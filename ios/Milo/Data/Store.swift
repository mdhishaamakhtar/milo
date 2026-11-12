import Foundation
import GRDB

// What the screens read, and the shapes they read it in. Writes are in StoreWrites.

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
        let matches: [(match: ExerciseMatch, score: Int)] = try allExercises(db).compactMap { e in
            let haystack = ([e.name] + e.aliases).joined(separator: " ").lowercased()
            guard words.allSatisfy({ haystack.contains($0) }) else { return nil }
            let last = try lastTime(db, exerciseId: e.id, excluding: nil)
            let match = ExerciseMatch(exercise: e, lastDate: last?.0, lastText: last.map { Format.setsLine($0.1, e.measurement) })
            // A name that starts with what was typed beats one that only contains it; your own beat built-ins.
            let score = (e.name.lowercased().hasPrefix(words[0]) ? 4 : 0) + (e.builtin ? 0 : 2)
            return (match, score)
        }
        // Done most recently first, then the better match.
        return Array(matches.sorted { ($0.match.lastDate ?? .distantPast, $0.score) > ($1.match.lastDate ?? .distantPast, $1.score) }.prefix(limit).map(\.match))
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
}
