import Foundation
import GRDB
import Observation
import OSLog

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

    /// The workout cover is up. The workout itself is whichever one is running.
    var showingWorkout = false
    var showingMilo = false
    var showingProfile = false
    /// What Milo is told about where the user is when they ask.
    var miloContext = MiloContext.today

    init(db: AppDatabase) {
        self.db = db
        api = APIClient(baseURL: APIClient.configuredURL)
        session = Session(db: db)
        sync = SyncEngine(db: db, api: api, session: session)
        health = HealthService(db: db)
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

extension AppModel {
    /// Deletes a workout here and, if it was saved there, from Apple Health.
    func deleteWorkout(_ workout: Workout) {
        do {
            try db.deleteWorkout(workout.id)
            Task { [health] in await health.remove(workout.healthUUID) }
            edited()
        } catch {
            log.error("delete workout: \(error.localizedDescription)")
        }
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
