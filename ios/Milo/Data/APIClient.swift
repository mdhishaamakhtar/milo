import Connect
import Foundation
import MiloAPI

/// The Milo API. One client per app; the session's token goes on each call.
final class APIClient: Sendable {
    let baseURL: String
    let auth: Milo_AuthServiceClient
    let sync: Milo_SyncServiceClient
    let milo: Milo_MiloServiceClient

    /// `http` stands in for the network in tests.
    init(baseURL: String, http: (any HTTPClientInterface)? = nil) {
        self.baseURL = baseURL
        let session = URLSessionConfiguration.default
        session.timeoutIntervalForRequest = 30
        session.waitsForConnectivity = false
        let client = ProtocolClient(
            httpClient: http ?? URLSessionHTTPClient(configuration: session),
            config: ProtocolClientConfig(host: baseURL, networkProtocol: .connect, codec: ProtoCodec())
        )
        auth = Milo_AuthServiceClient(client: client)
        sync = Milo_SyncServiceClient(client: client)
        milo = Milo_MiloServiceClient(client: client)
    }

    /// The server address for this build (Debug: this Mac; Release: Railway).
    static var configuredURL: String {
        (Bundle.main.object(forInfoDictionaryKey: "MILO_API_URL") as? String) ?? "http://localhost:8080"
    }

    static func headers(_ token: String?) -> Headers {
        guard let token else { return [:] }
        return ["Authorization": ["Bearer \(token)"]]
    }
}

/// An error worth showing a person.
struct APIError: Error, LocalizedError {
    let code: Code
    let message: String

    var errorDescription: String? { message }
    var isUnauthenticated: Bool { code == .unauthenticated }

    init(_ e: ConnectError) {
        code = e.code
        switch e.code {
        case .unavailable, .deadlineExceeded:
            message = "Can't reach Milo's server right now."
        default:
            message = e.message?.isEmpty == false ? e.message! : "Something went wrong (\(e.code))."
        }
    }
}

extension ResponseMessage {
    /// The response, or a readable error.
    func value() throws -> Output {
        switch result {
        case .success(let output): return output
        case .failure(let error): throw APIError(error)
        }
    }
}
