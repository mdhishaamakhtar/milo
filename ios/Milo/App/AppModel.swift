import Foundation
import GRDB
import MiloAPI
import Observation
import OSLog
import UserNotifications

/// Everything the screens share: the store, the API, the session, sync, and
/// which full-screen pieces are showing.
@Observable
final class AppModel {
    let db: AppDatabase
    let api: APIClient
    let session: Session
    let sync: SyncEngine
    let connectivity = Connectivity()
    let health: HealthService
    let rest = RestTimer()
    /// The conversation with Milo, kept while the app runs so closing the sheet doesn't lose it.
    @ObservationIgnored private(set) lazy var milo = MiloSession(app: self)

    /// The tab on screen, so a link like Today's "All history" can switch to it.
    var tab = TabID.today
    enum TabID { case today, history, progress }
    /// The workout cover is up. The workout itself is whichever one is running.
    var showingWorkout = false
    var showingMilo = false
    var showingProfile = false
    /// Milo started a workout: it opens once Milo's sheet is out of the way.
    var workoutStartedByMilo = false
    /// What Milo is told about where the user is when they ask.
    var miloContext = MiloContext.today
    /// The last delete, offered back for a few seconds instead of asking first.
    var undo: Undo?

    init(db: AppDatabase) {
        self.db = db
        api = APIClient(baseURL: APIClient.configuredURL)
        session = Session(db: db)
        sync = SyncEngine(db: db, api: api, session: session)
        health = HealthService(db: db)
        UNUserNotificationCenter.current().delegate = rest
        rest.openWorkout = { [weak self] in
            guard let self else { return }
            showingWorkout = (try? db.writer.read { try db.runningWorkout($0) }) != nil
        }
    }

    /// Call after any edit: syncs a couple of seconds later.
    func edited() {
        sync.soon()
    }

    /// Starts a workout, from a template or a past workout's exercises. One runs at a time:
    /// if one is already going, this opens it instead.
    func startWorkout(template: Template? = nil, copying workoutID: UUID? = nil) {
        do {
            if try db.writer.read({ try db.runningWorkout($0) }) == nil {
                if let workoutID, let past = try db.writer.read({ try Workout.fetchOne($0, key: workoutID) }) {
                    try db.startWorkout(name: past.name, items: try db.items(ofWorkout: workoutID), templateId: past.templateId)
                } else {
                    try db.startWorkout(template: template)
                }
                edited()
            }
            showingWorkout = true
        } catch {
            log.error("start workout: \(error.localizedDescription)")
        }
    }
}

/// "Deleted Legs, Undo". Deletes happen at once and can be taken back, which is Apple's
/// guidance for common destructive actions: undo, rather than a dialog before every one.
struct Undo: Identifiable {
    let id = UUID()
    let message: String
    let restore: () -> Void
}

extension AppModel {
    /// Deletes a workout here and, if it was saved there, from Apple Health. Undo puts it
    /// back in both.
    func deleteWorkout(_ workout: Workout, message: String? = nil) {
        do {
            let rows = try db.deleteWorkout(workout.id)
            Task { [health] in await health.remove(workout.healthUUID) }
            edited()
            offerUndo(message ?? "Deleted \(workout.name)", rows: rows) { [health] in
                if workout.healthUUID != nil { Task { await health.save(workoutID: workout.id) } }
            }
        } catch {
            log.error("delete workout: \(error.localizedDescription)")
        }
    }

    /// Shows the Undo bar for rows just deleted. `then` runs after they're back.
    func offerUndo(_ message: String, rows: [any SyncedRecord], then: @escaping () -> Void = {}) {
        guard !rows.isEmpty else { return }
        undo = Undo(message: message) { [weak self] in
            guard let self else { return }
            do {
                try db.restore(rows)
                then()
                edited()
            } catch {
                log.error("undo: \(error.localizedDescription)")
            }
        }
    }
}

extension AppModel {
    /// Runs a change to rows Milo proposed (applying a card, or undoing it), then does
    /// what the app's own buttons do around such a change: Apple Health hears about
    /// workouts that finished, changed or went, and the change is synced.
    func following<T>(_ card: Milo_Card, _ change: () throws -> T) throws -> T {
        let ids = card.workoutIDs
        let before = try workouts(ids)
        let result = try change()
        let after = try workouts(ids)
        Task { [health] in
            for id in ids { await health.follow(before: before[id], after: after[id]) }
        }
        edited()
        return result
    }

    private func workouts(_ ids: Set<UUID>) throws -> [UUID: Workout] {
        try db.writer.read { db in
            Dictionary(uniqueKeysWithValues: try Workout.fetchAll(db, keys: Array(ids)).map { ($0.id, $0) })
        }
    }

    /// Opens the workout Milo started, once its sheet has closed.
    func miloClosed() {
        guard workoutStartedByMilo else { return }
        workoutStartedByMilo = false
        showingWorkout = (try? db.writer.read { try db.runningWorkout($0) }) != nil
    }
}

private extension Milo_Card {
    /// The workouts a card touches: the one it names, and any whose row it writes.
    var workoutIDs: Set<UUID> {
        Set(([workoutID] + changes.map(\.workout.id)).compactMap { UUID(protoID: $0) })
    }
}

/// Where the user is, for the Ask Milo bar's prompt and for Milo's context.
struct MiloContext: Equatable {
    var screen: String
    var prompt: String
    var workoutID: UUID?
    var exerciseID: UUID?
    var chip: String?

    static let today = MiloContext(screen: "today", prompt: "Ask Milo, or tell it what you did")
    static let history = MiloContext(screen: "history", prompt: "Ask about any workout or date")
    static let progress = MiloContext(screen: "progress", prompt: "Ask about your progress")

    static func workout(_ id: UUID, name: String, day: String) -> MiloContext {
        MiloContext(screen: "workout_detail", prompt: "Ask about this workout, or fix it", workoutID: id, chip: "\(name), \(day)")
    }

    static func exercise(_ e: Exercise) -> MiloContext {
        MiloContext(screen: "exercise", prompt: "Ask about \(e.shortName.lowercased())", exerciseID: e.id, chip: e.shortName)
    }
}
