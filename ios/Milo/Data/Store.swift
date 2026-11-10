import Foundation
import GRDB

// Everything the screens read and write. Writes stamp updatedAt and mark rows
// dirty for the next sync; nothing here waits on the network.

/// One exercise inside a workout, with what the screen needs around it.
nonisolated struct ExerciseEntry: Identifiable, Hashable, Sendable {
    var we: WorkoutExercise
    var exercise: Exercise
    var sets: [LoggedSet]
    var unit: WeightUnit
    var restS: Int?
    /// Done sets from the last finished workout with this exercise.
    var lastTime: [LoggedSet]
    var lastDate: Date?

    var id: UUID { we.id }
    var doneSets: [LoggedSet] { sets.filter(\.done) }
    var isFinished: Bool { !sets.isEmpty && sets.allSatisfy(\.done) }
    var inputs: [Maths.SetInput] { sets.map { $0.input(exercise.measurement) } }
}

nonisolated struct WorkoutDetail: Identifiable, Hashable, Sendable {
    var workout: Workout
    var entries: [ExerciseEntry]
    var id: UUID { workout.id }

    var doneSetCount: Int { entries.reduce(0) { $0 + $1.doneSets.count } }
    var volumeKg: Double { entries.reduce(0) { $0 + Maths.volumeKg($1.inputs) } }
}

nonisolated struct WorkoutSummary: Identifiable, Hashable, Sendable {
    var workout: Workout
    var exerciseNames: [String]
    var exerciseCount: Int { exerciseNames.count }
    var doneSets: Int
    var volumeKg: Double
    var id: UUID { workout.id }
    var minutes: Int? {
        guard let end = workout.endedAt else { return nil }
        return max(1, Int(end.timeIntervalSince(workout.startedAt) / 60 + 0.5))
    }
}

nonisolated struct TemplateSummary: Identifiable, Hashable, Sendable {
    var template: Template
    var exerciseNames: [String]
    var lastUsed: Date?
    var id: UUID { template.id }
}

nonisolated struct ExerciseMatch: Identifiable, Hashable, Sendable {
    var exercise: Exercise
    var lastDate: Date?
    var lastText: String?
    var id: UUID { exercise.id }
}

nonisolated extension AppDatabase {
    // MARK: Reads

    func settings(_ db: Database) throws -> UserSettings {
        try UserSettings.fetchOne(db, key: 1) ?? .fresh
    }

    func latestBodyKg(_ db: Database, before date: Date = .distantFuture) throws -> Double? {
        try BodyMeasurement
            .filter(Column("deleted") == false && Column("weightKg") != nil && Column("measuredAt") <= date)
            .order(Column("measuredAt").desc)
            .fetchOne(db)?.weightKg
    }

    func runningWorkout(_ db: Database) throws -> Workout? {
        try Workout.filter(Column("deleted") == false && Column("endedAt") == nil).order(Column("startedAt").desc).fetchOne(db)
    }

    func unit(_ db: Database, for exerciseId: UUID) throws -> (WeightUnit, Int?) {
        if let pref = try ExercisePref.fetchOne(db, key: exerciseId), !pref.deleted {
            return (pref.unit, pref.restS)
        }
        return (try settings(db).displayUnit, nil)
    }

    /// The last finished workout's done sets for an exercise.
    func lastTime(_ db: Database, exerciseId: UUID, excluding workoutId: UUID?) throws -> (Date, [LoggedSet])? {
        let row = try SQLRequest<Row_>(sql: """
            SELECT we.id AS weId, w.startedAt AS startedAt FROM workoutExercise we JOIN workout w ON w.id = we.workoutId
            WHERE we.exerciseId = ? AND we.deleted = 0 AND w.deleted = 0 AND w.endedAt IS NOT NULL AND w.id IS NOT ?
            ORDER BY w.startedAt DESC LIMIT 1
            """, arguments: [exerciseId, workoutId]).fetchOne(db)
        guard let row else { return nil }
        let sets = try LoggedSet
            .filter(Column("workoutExerciseId") == row.weId && Column("deleted") == false && Column("done") == true)
            .order(Column("position")).fetchAll(db)
        return sets.isEmpty ? nil : (row.startedAt, sets)
    }

    private struct Row_: FetchableRecord, Decodable { var weId: UUID; var startedAt: Date }

    func workoutDetail(_ db: Database, id: UUID) throws -> WorkoutDetail? {
        guard let workout = try Workout.fetchOne(db, key: id), !workout.deleted else { return nil }
        let wes = try WorkoutExercise.filter(Column("workoutId") == id && Column("deleted") == false).order(Column("position")).fetchAll(db)
        var entries: [ExerciseEntry] = []
        for we in wes {
            guard let exercise = try Exercise.fetchOne(db, key: we.exerciseId) else { continue }
            let sets = try LoggedSet.filter(Column("workoutExerciseId") == we.id && Column("deleted") == false).order(Column("position")).fetchAll(db)
            let (unit, rest) = try unit(db, for: exercise.id)
            let last = try lastTime(db, exerciseId: exercise.id, excluding: id)
            entries.append(ExerciseEntry(we: we, exercise: exercise, sets: sets, unit: unit, restS: rest, lastTime: last?.1 ?? [], lastDate: last?.0))
        }
        return WorkoutDetail(workout: workout, entries: entries)
    }

    func finishedWorkouts(_ db: Database, limit: Int? = nil) throws -> [WorkoutSummary] {
        var request = Workout.filter(Column("deleted") == false && Column("endedAt") != nil).order(Column("startedAt").desc)
        if let limit { request = request.limit(limit) }
        let workouts = try request.fetchAll(db)
        guard !workouts.isEmpty else { return [] }
        let ids = workouts.map(\.id)
        let wes = try WorkoutExercise.filter(ids.contains(Column("workoutId")) && Column("deleted") == false).fetchAll(db)
        let exercises = try Exercise.filter(Set(wes.map(\.exerciseId)).contains(Column("id"))).fetchAll(db)
        let byID = Dictionary(uniqueKeysWithValues: exercises.map { ($0.id, $0) })
        let sets = try LoggedSet.filter(wes.map(\.id).contains(Column("workoutExerciseId")) && Column("deleted") == false).fetchAll(db)
        let setsByWE = Dictionary(grouping: sets, by: \.workoutExerciseId)
        let wesByWorkout = Dictionary(grouping: wes, by: \.workoutId)
        return workouts.map { w in
            let mine = (wesByWorkout[w.id] ?? []).sorted { $0.position < $1.position }
            var volume = 0.0, done = 0
            for we in mine {
                let s = setsByWE[we.id] ?? []
                done += s.filter(\.done).count
                volume += Maths.volumeKg(s.map { $0.input(byID[we.exerciseId]?.measurement ?? .repsLoad) })
            }
            return WorkoutSummary(workout: w, exerciseNames: mine.map { byID[$0.exerciseId]?.name ?? "Exercise" }, doneSets: done, volumeKg: volume)
        }
    }

    func templates(_ db: Database) throws -> [TemplateSummary] {
        let templates = try Template.filter(Column("deleted") == false).order(Column("updatedAt").desc).fetchAll(db)
        return try templates.map { t in
            let names = try t.items.map { try Exercise.fetchOne(db, key: $0.exerciseId)?.name ?? "Exercise" }
            let lastUsed = try Workout.filter(Column("templateId") == t.id && Column("deleted") == false && Column("endedAt") != nil)
                .order(Column("startedAt").desc).fetchOne(db)?.startedAt
            return TemplateSummary(template: t, exerciseNames: names, lastUsed: lastUsed)
        }
    }

    func allExercises(_ db: Database) throws -> [Exercise] {
        try Exercise.filter(Column("deleted") == false).order(Column("name")).fetchAll(db)
    }

    /// Exercises matching typed text by name or alias, the user's own and recently used first.
    func search(_ db: Database, _ text: String, limit: Int = 8) throws -> [ExerciseMatch] {
        let words = text.lowercased().split(separator: " ").map(String.init).filter { !$0.isEmpty }
        guard !words.isEmpty else { return [] }
        let all = try allExercises(db)
        let scored: [(Exercise, Int)] = all.compactMap { e in
            let haystack = ([e.name] + e.aliases).joined(separator: " ").lowercased()
            guard words.allSatisfy({ haystack.contains($0) }) else { return nil }
            var score = 0
            if e.name.lowercased().hasPrefix(words[0]) { score += 4 }
            if !e.builtin { score += 2 }
            return (e, score)
        }
        var matches: [ExerciseMatch] = []
        for (e, score) in scored.sorted(by: { $0.1 > $1.1 }) {
            let last = try lastTime(db, exerciseId: e.id, excluding: nil)
            matches.append(ExerciseMatch(exercise: e, lastDate: last?.0, lastText: last.map { Format.setsLine($0.1, e.measurement) }))
            _ = score
        }
        return Array(matches.sorted { ($0.lastDate ?? .distantPast) > ($1.lastDate ?? .distantPast) }.prefix(limit))
    }

    /// The exercises most often done first in a workout.
    func usualFirsts(_ db: Database, limit: Int = 3) throws -> [ExerciseMatch] {
        let ids = try UUID.fetchAll(db, sql: """
            SELECT we.exerciseId FROM workoutExercise we JOIN workout w ON w.id = we.workoutId
            WHERE we.deleted = 0 AND w.deleted = 0 AND w.endedAt IS NOT NULL AND we.position = 1
            GROUP BY we.exerciseId ORDER BY count(*) DESC, max(w.startedAt) DESC LIMIT ?
            """, arguments: [limit])
        return try ids.compactMap { id in
            guard let e = try Exercise.fetchOne(db, key: id) else { return nil }
            let last = try lastTime(db, exerciseId: id, excluding: nil)
            return ExerciseMatch(exercise: e, lastDate: last?.0, lastText: last.map { Format.setsLine($0.1, e.measurement) })
        }
    }

    /// One exercise across workouts, newest first.
    func history(_ db: Database, exerciseId: UUID) throws -> [(workout: Workout, sets: [LoggedSet])] {
        let wes = try WorkoutExercise.fetchAll(db, sql: """
            SELECT we.* FROM workoutExercise we JOIN workout w ON w.id = we.workoutId
            WHERE we.exerciseId = ? AND we.deleted = 0 AND w.deleted = 0 AND w.endedAt IS NOT NULL
            ORDER BY w.startedAt DESC
            """, arguments: [exerciseId])
        return try wes.compactMap { we in
            guard let w = try Workout.fetchOne(db, key: we.workoutId) else { return nil }
            let sets = try LoggedSet.filter(Column("workoutExerciseId") == we.id && Column("deleted") == false).order(Column("position")).fetchAll(db)
            return (w, sets)
        }
    }

    /// Done sets per exercise across all finished workouts.
    func setCounts(_ db: Database) throws -> [UUID: Int] {
        let rows = try SQLRequest<Count_>(sql: """
            SELECT we.exerciseId AS exerciseId, count(s.id) AS n FROM loggedSet s
            JOIN workoutExercise we ON we.id = s.workoutExerciseId JOIN workout w ON w.id = we.workoutId
            WHERE s.deleted = 0 AND s.done = 1 AND we.deleted = 0 AND w.deleted = 0
            GROUP BY we.exerciseId
            """).fetchAll(db)
        return Dictionary(uniqueKeysWithValues: rows.map { ($0.exerciseId, $0.n) })
    }

    private struct Count_: FetchableRecord, Decodable { var exerciseId: UUID; var n: Int }

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

    func removeExercise(_ weId: UUID, now: Date = Date()) throws {
        try writer.write { db in
            guard let we = try WorkoutExercise.fetchOne(db, key: weId) else { return }
            try we.tombstone(at: now).save(db)
            for s in try LoggedSet.filter(Column("workoutExerciseId") == weId && Column("deleted") == false).fetchAll(db) {
                try s.tombstone(at: now).save(db)
            }
        }
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
    func deleteWorkout(_ id: UUID, now: Date = Date()) throws {
        try writer.write { db in
            guard let w = try Workout.fetchOne(db, key: id) else { return }
            try w.tombstone(at: now).save(db)
            for we in try WorkoutExercise.filter(Column("workoutId") == id && Column("deleted") == false).fetchAll(db) {
                try we.tombstone(at: now).save(db)
                for s in try LoggedSet.filter(Column("workoutExerciseId") == we.id && Column("deleted") == false).fetchAll(db) {
                    try s.tombstone(at: now).save(db)
                }
            }
        }
    }

    func save(_ template: Template, now: Date = Date()) throws {
        var t = template
        t.updatedAt = now
        t.dirty = true
        try writer.write { db in try t.save(db) }
    }

    func deleteTemplate(_ id: UUID, now: Date = Date()) throws {
        try writer.write { db in
            guard let t = try Template.fetchOne(db, key: id) else { return }
            try t.tombstone(at: now).save(db)
        }
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

/// What a past workout's exercise card says beside its sets.
nonisolated struct ExerciseInsight: Hashable, Sendable {
    /// The set that beat every earlier workout's heaviest.
    var heaviestSet: UUID?
    /// When an earlier workout went heavier: its date and top set.
    var earlierTop: LoggedSet?
    var earlierTopDate: Date?
}

nonisolated extension AppDatabase {
    /// "Heaviest yet" and "Top set on 10 Oct was…" for each loaded exercise in a workout.
    func insights(_ db: Database, for detail: WorkoutDetail) throws -> [UUID: ExerciseInsight] {
        let start = detail.workout.startedAt
        var out: [UUID: ExerciseInsight] = [:]
        for e in detail.entries where e.exercise.measurement == .repsLoad {
            func load(_ s: LoggedSet) -> Double? { s.isWorking && (s.reps ?? 0) > 0 ? Maths.toKg(s.weight, s.unit) : nil }
            var earlier: (kg: Double, date: Date, set: LoggedSet)?
            for past in try history(db, exerciseId: e.exercise.id) where past.workout.startedAt < start {
                for s in past.sets {
                    if let kg = load(s), kg > (earlier?.kg ?? 0) { earlier = (kg, past.workout.startedAt, s) }
                }
            }
            let mine = e.sets.compactMap { s in load(s).map { (set: s, kg: $0) } }
            guard let top = mine.map(\.kg).max(), let earlier else { continue }
            if top > earlier.kg + 1e-9 {
                out[e.id] = ExerciseInsight(heaviestSet: mine.first { $0.kg == top }?.set.id)
            } else if earlier.kg > top + 1e-9 {
                out[e.id] = ExerciseInsight(earlierTop: earlier.set, earlierTopDate: earlier.date)
            }
        }
        return out
    }
}

// MARK: Templates and exercises

nonisolated extension AppDatabase {
    /// Exercises often done in the same workouts as these, most often first.
    func companions(_ db: Database, of ids: [UUID], limit: Int = 2) throws -> [Exercise] {
        guard !ids.isEmpty else { return [] }
        let marks = Array(repeating: "?", count: ids.count).joined(separator: ",")
        let found = try UUID.fetchAll(db, sql: """
            SELECT b.exerciseId FROM workoutExercise a JOIN workoutExercise b ON b.workoutId = a.workoutId
            JOIN workout w ON w.id = a.workoutId
            WHERE a.exerciseId IN (\(marks)) AND b.exerciseId NOT IN (\(marks))
              AND a.deleted = 0 AND b.deleted = 0 AND w.deleted = 0
            GROUP BY b.exerciseId ORDER BY count(DISTINCT a.workoutId) DESC LIMIT ?
            """, arguments: StatementArguments(ids + ids + [limit] as [any DatabaseValueConvertible]))
        return try found.compactMap { id in try Exercise.fetchOne(db, key: id).flatMap { $0.deleted ? nil : $0 } }
    }

    /// How many finished workouts and templates use an exercise.
    func uses(_ db: Database, exerciseId: UUID) throws -> (workouts: Int, templates: Int) {
        let workouts = try Int.fetchOne(db, sql: """
            SELECT count(DISTINCT we.workoutId) FROM workoutExercise we JOIN workout w ON w.id = we.workoutId
            WHERE we.exerciseId = ? AND we.deleted = 0 AND w.deleted = 0
            """, arguments: [exerciseId]) ?? 0
        let templates = try Template.filter(Column("deleted") == false).fetchAll(db)
            .filter { $0.items.contains { $0.exerciseId == exerciseId } }.count
        return (workouts, templates)
    }

    /// A new exercise of your own.
    @discardableResult
    func createExercise(name: String, equipment: String, measurement: MeasurementKind, unilateral: Bool, primaryMuscles: [String] = [], now: Date = Date()) throws -> Exercise {
        let e = Exercise(
            id: UUID(), builtin: false, name: name, aliases: [], primaryMuscles: primaryMuscles, secondaryMuscles: [],
            movementPattern: "", equipment: equipment, measurement: measurement, unilateral: unilateral,
            met: nil, about: "", approximate: false, updatedAt: now, deleted: false, dirty: true)
        try writer.write { db in try e.save(db) }
        return e
    }

    func save(_ exercise: Exercise, now: Date = Date()) throws {
        guard !exercise.builtin else { return }
        var e = exercise
        e.updatedAt = now
        e.dirty = true
        try writer.write { db in try e.save(db) }
    }

    /// Takes one of your exercises off your list. Past workouts keep showing it.
    func deleteExercise(_ id: UUID, now: Date = Date()) throws {
        try writer.write { db in
            guard let e = try Exercise.fetchOne(db, key: id), !e.builtin else { return }
            try e.tombstone(at: now).save(db)
        }
    }
}
