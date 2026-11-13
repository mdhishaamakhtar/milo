import Foundation
import GRDB
import OSLog

/// The phone's copy of the log. Everything is stored here first and synced later,
/// so logging works with no signal.
nonisolated final class AppDatabase: Sendable {
    let writer: any DatabaseWriter

    init(_ writer: any DatabaseWriter) throws {
        self.writer = writer
        try migrator.migrate(writer)
    }

    /// The app's database file in Application Support.
    static func onDisk() throws -> AppDatabase {
        let folder = try FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
        return try AppDatabase(DatabasePool(path: folder.appendingPathComponent("milo.sqlite").path))
    }

    /// An empty database for tests and previews.
    static func inMemory() throws -> AppDatabase {
        try AppDatabase(DatabaseQueue())
    }

    private var migrator: DatabaseMigrator {
        var m = DatabaseMigrator()
        #if DEBUG
        m.eraseDatabaseOnSchemaChange = true
        #endif
        m.registerMigration("v1") { db in
            func synced(_ t: TableDefinition) {
                t.column("updatedAt", .datetime).notNull()
                t.column("deleted", .boolean).notNull().defaults(to: false)
                t.column("dirty", .boolean).notNull().defaults(to: false).indexed()
            }
            try db.create(table: "exercise") { t in
                t.primaryKey("id", .blob)
                t.column("builtin", .boolean).notNull()
                t.column("name", .text).notNull().collate(.localizedCaseInsensitiveCompare)
                t.column("aliases", .jsonText).notNull()
                t.column("primaryMuscles", .jsonText).notNull()
                t.column("secondaryMuscles", .jsonText).notNull()
                t.column("movementPattern", .text).notNull()
                t.column("equipment", .text).notNull()
                t.column("measurement", .text).notNull()
                t.column("unilateral", .boolean).notNull()
                t.column("met", .double)
                t.column("about", .text).notNull()
                t.column("approximate", .boolean).notNull()
                synced(t)
            }
            try db.create(table: "exercisePref") { t in
                t.primaryKey("exerciseId", .blob)
                t.column("unit", .text).notNull()
                t.column("restS", .integer)
                synced(t)
            }
            try db.create(table: "workout") { t in
                t.primaryKey("id", .blob)
                t.column("name", .text).notNull()
                t.column("notes", .text).notNull()
                t.column("startedAt", .datetime).notNull().indexed()
                t.column("endedAt", .datetime)
                t.column("templateId", .blob)
                t.column("healthUUID", .blob)
                synced(t)
            }
            try db.create(table: "workoutExercise") { t in
                t.primaryKey("id", .blob)
                t.column("workoutId", .blob).notNull().indexed()
                t.column("exerciseId", .blob).notNull().indexed()
                t.column("position", .integer).notNull()
                t.column("groupLabel", .text).notNull()
                t.column("notes", .text).notNull()
                synced(t)
            }
            try db.create(table: "loggedSet") { t in
                t.primaryKey("id", .blob)
                t.column("workoutExerciseId", .blob).notNull().indexed()
                t.column("position", .integer).notNull()
                t.column("kind", .text).notNull()
                t.column("reps", .integer)
                t.column("weight", .text).notNull()
                t.column("unit", .text).notNull()
                t.column("perSide", .boolean).notNull()
                t.column("durationS", .integer)
                t.column("distanceM", .double)
                t.column("rpe", .double)
                t.column("done", .boolean).notNull()
                synced(t)
            }
            try db.create(table: "template") { t in
                t.primaryKey("id", .blob)
                t.column("name", .text).notNull()
                t.column("notes", .text).notNull()
                t.column("items", .jsonText).notNull()
                synced(t)
            }
            try db.create(table: "bodyMeasurement") { t in
                t.primaryKey("id", .blob)
                t.column("measuredAt", .datetime).notNull().indexed()
                t.column("weightKg", .double)
                t.column("bodyFatPct", .double)
                t.column("source", .text).notNull()
                synced(t)
            }
            try db.create(table: "userSettings") { t in
                t.primaryKey("id", .integer)
                t.column("displayUnit", .text).notNull()
                t.column("restDefaultS", .integer).notNull()
                t.column("heightCm", .double)
                t.column("birthDate", .text).notNull()
                t.column("sex", .text).notNull()
                synced(t)
            }
            try db.create(table: "meta") { t in
                t.primaryKey("key", .text)
                t.column("value", .text).notNull()
            }
        }
        return m
    }

    /// Removes everything, for signing in as a different account.
    func eraseAll() throws {
        try writer.write { db in
            for table in ["exercise", "exercisePref", "workout", "workoutExercise", "loggedSet", "template", "bodyMeasurement", "userSettings", "meta"] {
                try db.execute(sql: "DELETE FROM \(table)")
            }
        }
    }
}

// MARK: - Meta

nonisolated extension AppDatabase {
    enum MetaKey: String {
        case cursor, userId, username
        /// What became of Milo's cards, not yet told to the server: JSON, card id to status.
        case cardReports
    }

    func meta(_ key: MetaKey) throws -> String? {
        try writer.read { db in try Meta.fetchOne(db, key: key.rawValue)?.value }
    }

    func setMeta(_ key: MetaKey, _ value: String?) throws {
        try writer.write { db in
            if let value {
                try Meta(key: key.rawValue, value: value).save(db)
            } else {
                _ = try Meta.deleteOne(db, key: key.rawValue)
            }
        }
    }
}

nonisolated let log = Logger(subsystem: "dev.hishaam.milo", category: "app")
