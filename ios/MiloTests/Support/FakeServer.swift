import Connect
import Foundation
import MiloAPI
import SwiftProtobuf

/// Milo's server, as the app sees it in tests. It stands in for Connect's HTTP transport,
/// so every call still goes through the generated clients, the codec and Connect's
/// envelopes: a test sees what the app really sends and how it reads each answer. Answers
/// come from queues a test fills; with nothing queued, sync answers "nothing new".
nonisolated final class FakeServer: HTTPClientInterface, @unchecked Sendable {
    /// A Connect error, as the real server returns it.
    struct Failure: Error, Sendable {
        var code: Code
        var message: String
    }

    /// One part of Milo's streamed answer.
    enum Part: Sendable {
        case text(String)
        case reading(String)
        case card(Milo_Card)
        case problem(String)
        case done(conversationID: String)
        /// The stream ends with this error instead of cleanly.
        case fail(Failure)
        /// The server waits here until the test opens the gate.
        case wait(Gate)
        /// An event this app doesn't know, as a newer server might send.
        case unknownEvent
    }

    private let lock = NSLock()
    private var syncReplies: [Result<Milo_SyncResponse, Failure>] = []
    private var sendReplies: [[Part]] = []
    private var conversations: [String: Milo_GetConversationResponse] = [:]
    private var syncGate: Gate?
    private var whileSyncing: (@Sendable () -> Void)?
    private var _syncRequests: [Milo_SyncRequest] = []
    private var _sent: [Milo_SendRequest] = []
    private var _statuses: [Milo_SetCardStatusRequest] = []
    private var _tokens: [String?] = []
    private var _calls: [String] = []

    private func locked<T>(_ body: () throws -> T) rethrows -> T {
        lock.lock()
        defer { lock.unlock() }
        return try body()
    }

    // MARK: What a test sets up

    func replyToSync(_ reply: Result<Milo_SyncResponse, Failure>) { locked { syncReplies.append(reply) } }
    func replyToSend(_ parts: [Part]) { locked { sendReplies.append(parts) } }
    func store(_ conversation: Milo_GetConversationResponse, id: String) { locked { conversations[id] = conversation } }
    /// The next sync waits for the gate before it answers.
    func holdNextSync(_ gate: Gate) { locked { syncGate = gate } }
    /// Runs while the next sync request is on the server, before it answers.
    func duringNextSync(_ body: @escaping @Sendable () -> Void) { locked { whileSyncing = body } }

    // MARK: What the app sent

    var syncRequests: [Milo_SyncRequest] { locked { _syncRequests } }
    var sent: [Milo_SendRequest] { locked { _sent } }
    var cardStatuses: [Milo_SetCardStatusRequest] { locked { _statuses } }
    /// The bearer token on each call, in order.
    var tokens: [String?] { locked { _tokens } }
    /// Each call's method name ("Sync", "Send"), in order.
    var calls: [String] { locked { _calls } }

    // MARK: HTTPClientInterface

    func unary(
        request: HTTPRequest<Data?>,
        onMetrics: @escaping @Sendable (HTTPMetrics) -> Void,
        onResponse: @escaping @Sendable (HTTPResponse) -> Void
    ) -> Cancelable {
        record(request)
        let response: HTTPResponse
        do {
            let body = request.message ?? Data()
            switch request.url.path {
            case "/milo.SyncService/Sync":
                let req = try Milo_SyncRequest(serializedBytes: body)
                let (reply, gate, during) = locked {
                    _syncRequests.append(req)
                    defer { syncGate = nil; whileSyncing = nil }
                    return (syncReplies.isEmpty ? .success(Self.nothingNew(after: req)) : syncReplies.removeFirst(), syncGate, whileSyncing)
                }
                let answer = try Self.answer(reply)
                DispatchQueue.global().async {
                    during?()
                    gate?.wait()
                    onResponse(answer)
                }
                return Cancelable {}
            case "/milo.MiloService/GetConversation":
                let req = try Milo_GetConversationRequest(serializedBytes: body)
                let found = locked { conversations[req.id] }
                response = try Self.answer(found.map { .success($0) } ?? .failure(Failure(code: .notFound, message: "No such conversation.")))
            case "/milo.MiloService/SetCardStatus":
                let req = try Milo_SetCardStatusRequest(serializedBytes: body)
                locked { _statuses.append(req) }
                response = try Self.answer(.success(Milo_SetCardStatusResponse()))
            default:
                response = Self.error(Failure(code: .unimplemented, message: request.url.path))
            }
        } catch {
            response = Self.error(Failure(code: .internalError, message: "\(error)"))
        }
        DispatchQueue.global().async { onResponse(response) }
        return Cancelable {}
    }

    func stream(request: HTTPRequest<Data?>, responseCallbacks: ResponseCallbacks) -> RequestCallbacks<Data> {
        record(request)
        let body = Locked(Data())
        return RequestCallbacks(
            cancel: {},
            sendData: { data in body.update { $0.append(data) } },
            sendClose: { [self] in
                // Milo's Send is the one streaming call: one request in, a stream of events out.
                let parts: [Part]
                if let req = try? Milo_SendRequest(serializedBytes: body.value.dropFirst(5)) {
                    parts = locked {
                        _sent.append(req)
                        return sendReplies.isEmpty ? [.fail(Failure(code: .unavailable, message: ""))] : sendReplies.removeFirst()
                    }
                } else {
                    parts = [.fail(Failure(code: .invalidArgument, message: "bad request"))]
                }
                DispatchQueue.global().async {
                    responseCallbacks.receiveResponseHeaders(["content-type": ["application/connect+proto"]])
                    var failure: Failure?
                    for part in parts {
                        var event = Milo_SendResponse()
                        switch part {
                        case .text(let t): event.textDelta.text = t
                        case .reading(let r): event.reading.summary = r
                        case .card(let c): event.card = c
                        case .problem(let m): event.error.message = m
                        case .done(let id): event.done.conversationID = id
                        case .fail(let f): failure = f; continue
                        case .wait(let gate): gate.wait(); continue
                        case .unknownEvent: break
                        }
                        responseCallbacks.receiveResponseData(Self.envelope(try! event.serializedData(), flags: 0))
                    }
                    responseCallbacks.receiveResponseData(Self.envelope(Self.endOfStream(failure), flags: 0b10))
                    responseCallbacks.receiveClose(.ok, [:], nil)
                }
            })
    }

    // MARK: Wire format

    private func record(_ request: HTTPRequest<Data?>) {
        let auth = request.headers.first { $0.key.lowercased() == "authorization" }?.value.first
        locked {
            _tokens.append(auth.map { String($0.dropFirst("Bearer ".count)) })
            _calls.append(request.url.lastPathComponent)
        }
    }

    private static func nothingNew(after req: Milo_SyncRequest) -> Milo_SyncResponse {
        var res = Milo_SyncResponse()
        res.cursor = req.cursor
        return res
    }

    private static func answer<M: SwiftProtobuf.Message>(_ result: Result<M, Failure>) throws -> HTTPResponse {
        switch result {
        case .success(let message):
            return HTTPResponse(
                code: .ok, headers: ["content-type": ["application/proto"]], message: try message.serializedData(),
                trailers: [:], error: nil, tracingInfo: .init(httpStatus: 200))
        case .failure(let failure):
            return error(failure)
        }
    }

    private static func error(_ f: Failure) -> HTTPResponse {
        HTTPResponse(
            code: f.code, headers: ["content-type": ["application/json"]], message: json(f),
            trailers: [:], error: nil, tracingInfo: .init(httpStatus: 400))
    }

    private static func json(_ f: Failure) -> Data {
        let body: [String: String] = ["code": f.code.name, "message": f.message]
        return try! JSONSerialization.data(withJSONObject: body)
    }

    private static func endOfStream(_ failure: Failure?) -> Data {
        guard let failure else { return Data("{}".utf8) }
        return try! JSONSerialization.data(withJSONObject: ["error": ["code": failure.code.name, "message": failure.message]])
    }

    /// Connect's envelope: a flags byte, the length as four big-endian bytes, the message.
    private static func envelope(_ message: Data, flags: UInt8) -> Data {
        var out = Data([flags])
        withUnsafeBytes(of: UInt32(message.count).bigEndian) { out.append(contentsOf: $0) }
        out.append(message)
        return out
    }
}

/// Holds the fake server at a point until the test lets it go on.
nonisolated final class Gate: Sendable {
    private let semaphore = DispatchSemaphore(value: 0)
    func open() { semaphore.signal() }
    func wait() { semaphore.wait() }
}

/// Waits for something the app does on its own time (a stream's next event, a status
/// reported in the background), failing after a few seconds instead of hanging.
@MainActor
func eventually(_ what: String = "condition", _ condition: () -> Bool) async throws {
    let deadline = ContinuousClock.now + .seconds(5)
    while !condition() {
        guard ContinuousClock.now < deadline else { throw Timeout(what: what) }
        try await Task.sleep(for: .milliseconds(5))
    }
}

struct Timeout: Error { let what: String }

/// A value shared between Connect's callbacks.
nonisolated final class Locked<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var _value: Value
    init(_ value: Value) { _value = value }
    var value: Value { lock.lock(); defer { lock.unlock() }; return _value }
    func update(_ body: (inout Value) -> Void) { lock.lock(); defer { lock.unlock() }; body(&_value) }
}
