import Foundation
import GRDB
import Observation
import OSLog

/// A value read from the database that updates whenever the rows behind it change.
@Observable
final class Live<Value: Sendable> {
    private(set) var value: Value
    @ObservationIgnored private var cancellable: AnyDatabaseCancellable?

    init(_ initial: Value) {
        value = initial
    }

    /// Starts (or restarts) watching. Call from a view's `.task` or `.onAppear`.
    func watch(_ db: AppDatabase, _ fetch: @escaping @Sendable (Database) throws -> Value) {
        cancellable = ValueObservation.tracking(fetch).start(
            in: db.writer,
            scheduling: .immediate,
            onError: { error in log.error("live query: \(error.localizedDescription, privacy: .public)") },
            onChange: { [weak self] value in
                MainActor.assumeIsolated { self?.value = value }
            }
        )
    }
}
