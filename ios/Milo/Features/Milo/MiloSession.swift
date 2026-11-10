import Connect
import Foundation
import MiloAPI
import SwiftProtobuf
import Observation
import OSLog

/// One conversation with Milo: what was said, what Milo read, and its cards.
/// Cards are applied here, on the phone, then synced like any other edit.
@Observable
final class MiloSession {
    enum Item: Identifiable {
        case user(UUID, String)
        case reply(UUID, String)
        case reading(UUID, String)
        case card(Milo_Card)
        case problem(UUID, String)

        var id: String {
            switch self {
            case .user(let id, _), .reply(let id, _), .reading(let id, _), .problem(let id, _): id.uuidString
            case .card(let c): c.id
            }
        }
    }

    enum CardState: Equatable { case pending, applied, dismissed, undone, stale }

    private(set) var items: [Item] = []
    private(set) var busy = false
    private(set) var conversationID = ""
    private(set) var cardStates: [String: CardState] = [:]
    /// The workout exercise the last applied card added, so the workout can open it.
    private(set) var addedWorkoutExercise: UUID?
    /// When something was last said, to start afresh after a long gap.
    private(set) var lastActive = Date.now
    @ObservationIgnored private var undoTokens: [String: ChangeApplier.UndoToken] = [:]
    private let app: AppModel

    init(app: AppModel) {
        self.app = app
    }

    var isEmpty: Bool { items.isEmpty }

    /// Waiting on Milo with nothing streaming in yet: it is reading, or deciding what to do.
    var isWorking: Bool {
        guard busy else { return false }
        switch items.last {
        case .reply: return false
        // What to say next comes after the answer: Milo is done.
        case .card(let card) where card.kind == .suggestions: return false
        default: return true
        }
    }

    /// Milo has read from the log since the last message: the wait is it going through it.
    var hasReadSinceAsking: Bool {
        guard let asked = items.lastIndex(where: { if case .user = $0 { true } else { false } }) else { return false }
        return items[(asked + 1)...].contains { if case .reading = $0 { true } else { false } }
    }

    func state(_ card: Milo_Card) -> CardState { cardStates[card.id] ?? .pending }

    // MARK: Talking

    func send(_ text: String, context: MiloContext) async {
        let text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !busy else { return }
        items.append(.user(UUID(), text))
        lastActive = .now
        guard app.connectivity.isOnline else {
            items.append(.problem(UUID(), "Milo needs a connection. Logging by hand still works."))
            return
        }
        busy = true
        defer { busy = false }
        await app.sync.sync() // so Milo reads what was just logged

        var request = Milo_SendRequest()
        request.conversationID = conversationID
        request.text = text
        request.context = proto(context)
        let stream = app.api.milo.send(headers: APIClient.headers(app.session.token))
        do {
            try stream.send(request)
        } catch {
            items.append(.problem(UUID(), "Couldn't reach Milo."))
            return
        }
        var replyID: UUID?
        for await result in stream.results() {
            switch result {
            case .message(let response):
                handle(response, replyID: &replyID)
            case .complete(_, let error, _):
                if let error = error as? ConnectError {
                    if error.code == .unauthenticated { app.session.tokenExpired() }
                    items.append(.problem(UUID(), APIError(error).message))
                }
            case .headers:
                break
            }
        }
    }

    private func handle(_ response: Milo_SendResponse, replyID: inout UUID?) {
        switch response.event {
        case .textDelta(let delta)?:
            if let id = replyID, let i = items.lastIndex(where: { $0.id == id.uuidString }), case .reply(_, let text) = items[i] {
                items[i] = .reply(id, text + delta.text)
            } else {
                let id = UUID()
                replyID = id
                items.append(.reply(id, delta.text))
            }
        case .reading(let reading)?:
            items.append(.reading(UUID(), reading.summary))
            replyID = nil
        case .card(let card)?:
            items.append(.card(card))
            replyID = nil
            if card.autoApply { apply(card) }
        case .error(let e)?:
            items.append(.problem(UUID(), e.message))
        case .done(let done)?:
            conversationID = done.conversationID
        case nil:
            break
        }
    }

    private func proto(_ c: MiloContext) -> Milo_MiloContext {
        var p = Milo_MiloContext()
        p.screen = c.screen
        p.workoutID = c.workoutID.map(Proto.id) ?? ""
        p.exerciseID = c.exerciseID.map(Proto.id) ?? ""
        p.timezone = TimeZone.current.identifier
        return p
    }

    // MARK: Cards

    @discardableResult
    func apply(_ card: Milo_Card) -> Bool {
        do {
            let token = try app.following(card) { try ChangeApplier(db: app.db).apply(card.changes, base: card.base) }
            undoTokens[card.id] = token
            cardStates[card.id] = .applied
            if let we = card.changes.first(where: { !$0.workoutExercise.id.isEmpty }) {
                addedWorkoutExercise = UUID(protoID: we.workoutExercise.id)
            }
            if token.created.contains(where: { ($0 as? Workout).map { $0.endedAt == nil } ?? false }) {
                app.workoutStartedByMilo = true
            }
            report(card, .applied)
            return true
        } catch ChangeApplier.ApplyError.stale {
            cardStates[card.id] = .stale
            return false
        } catch {
            log.error("apply card: \(error.localizedDescription)")
            return false
        }
    }

    func undo(_ card: Milo_Card) {
        guard let token = undoTokens[card.id] else { return }
        do {
            try app.following(card) { try ChangeApplier(db: app.db).undo(token) }
            undoTokens[card.id] = nil
            cardStates[card.id] = .undone
            if token.created.contains(where: { $0 is Workout }) { app.workoutStartedByMilo = false }
            report(card, .undone)
        } catch {
            log.error("undo card: \(error.localizedDescription)")
        }
    }

    func dismiss(_ card: Milo_Card) {
        cardStates[card.id] = .dismissed
        report(card, .dismissed)
    }

    private func report(_ card: Milo_Card, _ status: Milo_CardStatus) {
        let api = app.api, token = app.session.token
        Task {
            var req = Milo_SetCardStatusRequest()
            req.cardID = card.id
            req.status = status
            _ = await api.milo.setCardStatus(request: req, headers: APIClient.headers(token))
        }
    }

    // MARK: Earlier conversations

    func open(_ id: String) async {
        var req = Milo_GetConversationRequest()
        req.id = id
        guard let res = try? await app.api.milo.getConversation(request: req, headers: APIClient.headers(app.session.token)).value() else { return }
        conversationID = id
        lastActive = .now
        cardStates = [:]
        undoTokens = [:]
        items = res.items.compactMap { item in
            switch item.item {
            case .userText(let t)?: .user(UUID(), t)
            case .assistantText(let t)?: .reply(UUID(), t)
            case .reading(let r)?: .reading(UUID(), r.summary)
            case .card(let c)?:
                {
                    cardStates[c.id] = switch c.status {
                    case .applied: .applied
                    case .dismissed: .dismissed
                    case .undone: .undone
                    default: .pending
                    }
                    return .card(c)
                }()
            case nil: nil
            }
        }
    }

    /// Left alone for half an hour, a conversation is over: Milo opens fresh.
    /// The old one is still under Chats.
    func resetIfIdle(now: Date = .now) {
        if !busy, now.timeIntervalSince(lastActive) > 30 * 60 { reset() }
    }

    func reset() {
        items = []
        conversationID = ""
        cardStates = [:]
        undoTokens = [:]
    }
}
