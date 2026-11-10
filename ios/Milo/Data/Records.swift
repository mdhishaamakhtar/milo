import Foundation
import GRDB
import MiloAPI

// Local rows, mirroring the server's. Every synced row carries updatedAt (the
// newest write wins), deleted (a tombstone) and dirty (changed here, not pushed yet).

nonisolated protocol SyncedRecord: Codable, FetchableRecord, PersistableRecord, Sendable {
    /// The table name the server and Milo's cards use.
    static var syncTable: String { get }
    var syncKey: UUID { get }
    var updatedAt: Date { get set }
    var deleted: Bool { get set }
    var dirty: Bool { get set }
    /// The row as the API sends it.
    var change: Milo_Change { get }
    static func stored(_ db: Database, key: UUID) throws -> Self?
}

nonisolated extension SyncedRecord {
    static func stored(_ db: Database, key: UUID) throws -> Self? { try fetchOne(db, key: key) }

    func stamped(_ date: Date, dirty: Bool) -> Self {
        var r = self
        r.updatedAt = date
        r.dirty = dirty
        return r
    }

    func tombstone(at date: Date) -> Self {
        var r = self
        r.deleted = true
        r.updatedAt = date
        r.dirty = true
        return r
    }
}

/// Every synced table, in push order.
nonisolated enum Synced {
    static let types: [any SyncedRecord.Type] = [
        Exercise.self, ExercisePref.self, Workout.self, WorkoutExercise.self, LoggedSet.self,
        Template.self, BodyMeasurement.self, UserSettings.self,
    ]

    /// The stored row with a table name and key, for checking a card's base versions.
    static func stored(_ db: Database, table: String, key: UUID) throws -> (any SyncedRecord)? {
        switch table {
        case Exercise.syncTable: try Exercise.stored(db, key: key)
        case ExercisePref.syncTable: try ExercisePref.stored(db, key: key)
        case Workout.syncTable: try Workout.stored(db, key: key)
        case WorkoutExercise.syncTable: try WorkoutExercise.stored(db, key: key)
        case LoggedSet.syncTable: try LoggedSet.stored(db, key: key)
        case Template.syncTable: try Template.stored(db, key: key)
        case BodyMeasurement.syncTable: try BodyMeasurement.stored(db, key: key)
        case UserSettings.syncTable: try UserSettings.stored(db, key: key)
        default: nil
        }
    }

    /// The stored version of a record, if any.
    static func stored(_ db: Database, of record: some SyncedRecord) throws -> (any SyncedRecord)? {
        try type(of: record).stored(db, key: record.syncKey)
    }

    /// Rows changed here and not yet pushed.
    static func dirty(_ db: Database, limit: Int) throws -> [any SyncedRecord] {
        func rows<T: SyncedRecord>(_: T.Type) throws -> [any SyncedRecord] {
            try T.filter(Column("dirty") == true).limit(limit).fetchAll(db)
        }
        return Array(try types.flatMap { try rows($0) }.prefix(limit))
    }
}

nonisolated struct Exercise: Identifiable, Hashable, SyncedRecord {
    static let databaseTableName = "exercise"
    static let syncTable = "exercise"
    var id: UUID
    var builtin: Bool
    var name: String
    var aliases: [String]
    var primaryMuscles: [String]
    var secondaryMuscles: [String]
    var movementPattern: String
    var equipment: String
    var measurement: MeasurementKind
    var unilateral: Bool
    var met: Double?
    var about: String
    var approximate: Bool
    var updatedAt: Date
    var deleted: Bool
    var dirty: Bool
    var syncKey: UUID { id }

    /// "Leg Extension (Machine)" → "Leg Extension".
    var shortName: String {
        guard let i = name.range(of: " (") else { return name }
        return String(name[..<i.lowerBound])
    }

    /// "Machine", from "Leg Extension (Machine)", else the equipment.
    var equipmentLabel: String {
        if let open = name.range(of: " ("), let close = name.range(of: ")", options: .backwards), open.upperBound < close.lowerBound {
            return String(name[open.upperBound..<close.lowerBound])
        }
        return equipment.capitalized
    }
}

nonisolated struct ExercisePref: Hashable, SyncedRecord {
    static let databaseTableName = "exercisePref"
    static let syncTable = "exercise_pref"
    var exerciseId: UUID
    var unit: WeightUnit
    var restS: Int?
    var updatedAt: Date
    var deleted: Bool
    var dirty: Bool
    var syncKey: UUID { exerciseId }
}

nonisolated struct Workout: Identifiable, Hashable, SyncedRecord {
    static let databaseTableName = "workout"
    static let syncTable = "workout"
    var id: UUID
    var name: String
    var notes: String
    var startedAt: Date
    var endedAt: Date?
    var templateId: UUID?
    var healthUUID: UUID?
    var updatedAt: Date
    var deleted: Bool
    var dirty: Bool
    var syncKey: UUID { id }
}

nonisolated struct WorkoutExercise: Identifiable, Hashable, SyncedRecord {
    static let databaseTableName = "workoutExercise"
    static let syncTable = "workout_exercise"
    var id: UUID
    var workoutId: UUID
    var exerciseId: UUID
    var position: Int
    var groupLabel: String
    var notes: String
    var updatedAt: Date
    var deleted: Bool
    var dirty: Bool
    var syncKey: UUID { id }
}

nonisolated struct LoggedSet: Identifiable, Hashable, SyncedRecord {
    static let databaseTableName = "loggedSet"
    static let syncTable = "set"
    var id: UUID
    var workoutExerciseId: UUID
    var position: Int
    var kind: SetKind
    var reps: Int?
    /// Exact weight as typed, in `unit`; "" when there is none.
    var weight: String
    var unit: WeightUnit
    var perSide: Bool
    var durationS: Int?
    var distanceM: Double?
    var rpe: Double?
    var done: Bool
    var updatedAt: Date
    var deleted: Bool
    var dirty: Bool
    var syncKey: UUID { id }

    func input(_ measurement: MeasurementKind) -> Maths.SetInput {
        Maths.SetInput(measurement: measurement, weight: weight, unit: unit, reps: reps ?? 0, perSide: perSide, done: done, kind: kind)
    }
}

nonisolated struct TemplateItem: Codable, Hashable, Sendable {
    var exerciseId: UUID
    var groupLabel: String
}

nonisolated struct Template: Identifiable, Hashable, SyncedRecord {
    static let databaseTableName = "template"
    static let syncTable = "template"
    var id: UUID
    var name: String
    var notes: String
    var items: [TemplateItem]
    var updatedAt: Date
    var deleted: Bool
    var dirty: Bool
    var syncKey: UUID { id }
}

nonisolated struct BodyMeasurement: Identifiable, Hashable, SyncedRecord {
    static let databaseTableName = "bodyMeasurement"
    static let syncTable = "body_measurement"
    var id: UUID
    var measuredAt: Date
    var weightKg: Double?
    var bodyFatPct: Double?
    var source: String
    var updatedAt: Date
    var deleted: Bool
    var dirty: Bool
    var syncKey: UUID { id }
}

/// One row per account.
nonisolated struct UserSettings: Hashable, SyncedRecord {
    static let databaseTableName = "userSettings"
    static let syncTable = "settings"
    var id = 1
    var displayUnit: WeightUnit
    var restDefaultS: Int
    var heightCm: Double?
    var birthDate: String
    var sex: String
    var updatedAt: Date
    var deleted = false
    var dirty: Bool
    var syncKey: UUID { UUID(uuid: (0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1)) }

    static func stored(_ db: Database, key _: UUID) throws -> UserSettings? { try fetchOne(db, key: 1) }

    static let fresh = UserSettings(displayUnit: .kg, restDefaultS: 60, heightCm: nil, birthDate: "", sex: "", updatedAt: .distantPast, dirty: false)
}

/// Small key/value store: the sync cursor and the signed-in user.
nonisolated struct Meta: Codable, FetchableRecord, PersistableRecord {
    static let databaseTableName = "meta"
    var key: String
    var value: String
}
