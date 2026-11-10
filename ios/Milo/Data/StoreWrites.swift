import Foundation
import GRDB

// What the screens write. Every write stamps updatedAt and marks the row dirty
// for the next sync, in one transaction; nothing here waits on the network.

nonisolated extension AppDatabase {
    // MARK: Writes

    @discardableResult
    func startWorkout(name: String? = nil, template: Template? = nil, now: Date = Date()) throws -> Workout {
        try startWorkout(name: name ?? template?.name, items: template?.items ?? [], templateId: template?.id, now: now)
    }

    /// A new workout with these exercises, numbers filled in from last time.
    @discardableResult
    func startWorkout(name: String?, items: [TemplateItem], templateId: UUID? = nil, now: Date = Date()) throws -> Workout {
        try writer.write { db in
            let workout = Workout(
                id: UUID(), name: name ?? Format.defaultWorkoutName(now), notes: "", startedAt: now, endedAt: nil,
                templateId: templateId, healthUUID: nil, updatedAt: now, deleted: false, dirty: true)
            try workout.save(db)
            for (i, item) in items.enumerated() {
                try insertExercise(db, item.exerciseId, into: workout.id, position: i + 1, groupLabel: item.groupLabel, now: now)
            }
            return workout
        }
    }

    /// A workout's exercises in order, supersets kept: for templates and "Do it again".
    func items(ofWorkout id: UUID) throws -> [TemplateItem] {
        try writer.read { db in
            try WorkoutExercise.filter(Column("workoutId") == id && Column("deleted") == false).order(Column("position")).fetchAll(db)
                .map { TemplateItem(exerciseId: $0.exerciseId, groupLabel: $0.groupLabel) }
        }
    }

    @discardableResult
    func addExercise(_ exerciseId: UUID, to workoutId: UUID, groupLabel: String = "", now: Date = Date()) throws -> WorkoutExercise {
        try writer.write { db in
            let position = (try Int.fetchOne(db, sql: "SELECT max(position) FROM workoutExercise WHERE workoutId = ? AND deleted = 0", arguments: [workoutId]) ?? 0) + 1
            return try insertExercise(db, exerciseId, into: workoutId, position: position, groupLabel: groupLabel, now: now)
        }
    }

    /// Adds an exercise with sets filled in from last time (not ticked), or one empty set.
    @discardableResult
    private func insertExercise(_ db: Database, _ exerciseId: UUID, into workoutId: UUID, position: Int, groupLabel: String, now: Date) throws -> WorkoutExercise {
        let we = WorkoutExercise(id: UUID(), workoutId: workoutId, exerciseId: exerciseId, position: position, groupLabel: groupLabel, notes: "", updatedAt: now, deleted: false, dirty: true)
        try we.save(db)
        let (unit, _) = try unit(db, for: exerciseId)
        let last = try lastTime(db, exerciseId: exerciseId, excluding: workoutId)?.1 ?? []
        let perSide = try Exercise.fetchOne(db, key: exerciseId)?.unilateral ?? false
        let templates: [LoggedSet] = last.isEmpty
            ? [LoggedSet(id: UUID(), workoutExerciseId: we.id, position: 1, kind: .normal, reps: nil, weight: "", unit: unit, perSide: perSide, durationS: nil, distanceM: nil, rpe: nil, done: false, updatedAt: now, deleted: false, dirty: true)]
            : last
        for (i, s) in templates.enumerated() {
            var new = s
            new.id = UUID()
            new.workoutExerciseId = we.id
            new.position = i + 1
            new.done = false
            new.updatedAt = now
            new.deleted = false
            new.dirty = true
            try new.save(db)
        }
        return we
    }

    /// A new set copying the one before it.
    @discardableResult
    func addSet(to weId: UUID, done: Bool = false, now: Date = Date()) throws -> LoggedSet {
        try writer.write { db in
            let previous = try LoggedSet.filter(Column("workoutExerciseId") == weId && Column("deleted") == false).order(Column("position").desc).fetchOne(db)
            let we = try WorkoutExercise.fetchOne(db, key: weId)
            let (unit, _) = try unit(db, for: we?.exerciseId ?? UUID())
            var set = previous ?? LoggedSet(id: UUID(), workoutExerciseId: weId, position: 0, kind: .normal, reps: nil, weight: "", unit: unit, perSide: false, durationS: nil, distanceM: nil, rpe: nil, done: false, updatedAt: now, deleted: false, dirty: true)
            set.id = UUID()
            set.position = (previous?.position ?? 0) + 1
            set.done = done
            set.updatedAt = now
            set.dirty = true
            try set.save(db)
            return set
        }
    }

    func update(_ set: LoggedSet, now: Date = Date()) throws {
        var s = set
        s.updatedAt = now
        s.dirty = true
        try writer.write { db in try s.save(db) }
    }

    /// Ticks or unticks a set; returns whether it is now done.
    @discardableResult
    func toggleDone(_ setId: UUID, now: Date = Date()) throws -> Bool {
        try writer.write { db in
            guard var s = try LoggedSet.fetchOne(db, key: setId) else { return false }
            s.done.toggle()
            s.updatedAt = now
            s.dirty = true
            try s.save(db)
            return s.done
        }
    }

    func deleteSet(_ id: UUID, now: Date = Date()) throws {
        try writer.write { db in
            guard let s = try LoggedSet.fetchOne(db, key: id) else { return }
            try s.tombstone(at: now).save(db)
        }
    }

    @discardableResult
    func removeExercise(_ weId: UUID, now: Date = Date()) throws -> [any SyncedRecord] {
        try writer.write { db in
            guard let we = try WorkoutExercise.fetchOne(db, key: weId) else { return [] }
            return try Self.tombstone(exercise: we, in: db, at: now)
        }
    }

    /// Deletes an exercise from a workout with its sets; returns them as they were.
    private static func tombstone(exercise we: WorkoutExercise, in db: Database, at now: Date) throws -> [any SyncedRecord] {
        var gone: [any SyncedRecord] = [we]
        try we.tombstone(at: now).save(db)
        for s in try LoggedSet.filter(Column("workoutExerciseId") == we.id && Column("deleted") == false).fetchAll(db) {
            gone.append(s)
            try s.tombstone(at: now).save(db)
        }
        return gone
    }

    /// Remembers the unit for an exercise. Stored sets keep the unit they were typed in;
    /// the screen shows them converted.
    func setUnit(_ unit: WeightUnit, for exerciseId: UUID, now: Date = Date()) throws {
        try writer.write { db in
            var pref = try ExercisePref.fetchOne(db, key: exerciseId) ?? ExercisePref(exerciseId: exerciseId, unit: unit, restS: nil, updatedAt: now, deleted: false, dirty: true)
            pref.unit = unit
            pref.deleted = false
            pref.updatedAt = now
            pref.dirty = true
            try pref.save(db)
        }
    }

    func setRest(_ seconds: Int?, for exerciseId: UUID, now: Date = Date()) throws {
        try writer.write { db in
            let (unit, _) = try unit(db, for: exerciseId)
            var pref = try ExercisePref.fetchOne(db, key: exerciseId) ?? ExercisePref(exerciseId: exerciseId, unit: unit, restS: nil, updatedAt: now, deleted: false, dirty: true)
            pref.restS = seconds
            pref.deleted = false
            pref.updatedAt = now
            pref.dirty = true
            try pref.save(db)
        }
    }

    func setNote(_ note: String, for weId: UUID, now: Date = Date()) throws {
        try writer.write { db in
            guard var we = try WorkoutExercise.fetchOne(db, key: weId) else { return }
            we.notes = note
            we.updatedAt = now
            we.dirty = true
            try we.save(db)
        }
    }

    func save(_ workout: Workout, now: Date = Date()) throws {
        var w = workout
        w.updatedAt = now
        w.dirty = true
        try writer.write { db in try w.save(db) }
    }

    /// Ends a workout, or saves edits to a finished one. Sets that were never ticked are
    /// dropped, and exercises left with none.
    func finishWorkout(_ id: UUID, name: String, notes: String, at end: Date = Date(), now: Date = Date()) throws {
        try writer.write { db in
            guard var w = try Workout.fetchOne(db, key: id) else { return }
            for we in try WorkoutExercise.filter(Column("workoutId") == id && Column("deleted") == false).fetchAll(db) {
                let sets = try LoggedSet.filter(Column("workoutExerciseId") == we.id && Column("deleted") == false).fetchAll(db)
                for s in sets where !s.done {
                    try s.tombstone(at: now).save(db)
                }
                if !sets.contains(where: \.done) {
                    try we.tombstone(at: now).save(db)
                }
            }
            w.name = name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? w.name : name
            w.notes = notes
            w.endedAt = end
            w.updatedAt = now
            w.dirty = true
            try w.save(db)
        }
    }

    /// Deletes a workout with its exercises and sets: discarding a running one, or deleting one from History.
    /// Returns the rows as they were, so the delete can be undone with `restore`.
    @discardableResult
    func deleteWorkout(_ id: UUID, now: Date = Date()) throws -> [any SyncedRecord] {
        try writer.write { db in
            guard let w = try Workout.fetchOne(db, key: id) else { return [] }
            var gone: [any SyncedRecord] = [w]
            try w.tombstone(at: now).save(db)
            for we in try WorkoutExercise.filter(Column("workoutId") == id && Column("deleted") == false).fetchAll(db) {
                gone += try Self.tombstone(exercise: we, in: db, at: now)
            }
            return gone
        }
    }

    func save(_ template: Template, now: Date = Date()) throws {
        var t = template
        t.updatedAt = now
        t.dirty = true
        try writer.write { db in try t.save(db) }
    }

    @discardableResult
    func deleteTemplate(_ id: UUID, now: Date = Date()) throws -> [any SyncedRecord] {
        try writer.write { db in
            guard let t = try Template.fetchOne(db, key: id) else { return [] }
            try t.tombstone(at: now).save(db)
            return [t]
        }
    }

    /// Puts deleted rows back as they were, stamped now so the restore syncs too.
    func restore(_ rows: [any SyncedRecord], now: Date = Date()) throws {
        try writer.write { db in
            for row in rows { try Self.put(row, in: db, at: now) }
        }
    }

    private static func put<R: SyncedRecord>(_ row: R, in db: Database, at now: Date) throws {
        try row.stamped(now, dirty: true).save(db)
    }

    /// A template from a workout's exercises, in order, supersets kept.
    @discardableResult
    func templateFrom(workout id: UUID, name: String, now: Date = Date()) throws -> Template {
        let t = Template(id: UUID(), name: name, notes: "", items: try items(ofWorkout: id), updatedAt: now, deleted: false, dirty: true)
        try save(t, now: now)
        return t
    }

    func save(_ settings: UserSettings, now: Date = Date()) throws {
        var s = settings
        s.updatedAt = now
        s.dirty = true
        try writer.write { db in try s.save(db) }
    }

    /// A weight or body-fat reading. One from Health keeps its Health id and is added only
    /// once, so a reading deleted here doesn't come back on the next import.
    func addBody(weightKg: Double?, bodyFatPct: Double?, at: Date, source: String, id: UUID = UUID(), now: Date = Date()) throws {
        let m = BodyMeasurement(id: id, measuredAt: at, weightKg: weightKg, bodyFatPct: bodyFatPct, source: source, updatedAt: now, deleted: false, dirty: true)
        try writer.write { db in
            if source == "health" { try m.insert(db, onConflict: .ignore) } else { try m.save(db) }
        }
    }

    func deleteBody(_ id: UUID, now: Date = Date()) throws {
        try writer.write { db in
            guard let m = try BodyMeasurement.fetchOne(db, key: id) else { return }
            try m.tombstone(at: now).save(db)
        }
    }
}
