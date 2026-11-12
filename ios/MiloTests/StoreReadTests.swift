import Foundation
import GRDB
import Testing
@testable import Milo

/// What the screens read from the phone's store.
struct StoreReadTests {
    private let now = Date(timeIntervalSince1970: 1_794_300_000) // Tuesday 10 November 2026

    @Test func settingsAreTheDefaultsUntilSaved() throws {
        let db = try freshDB()
        #expect(try db.writer.read { try db.settings($0) } == .fresh)
        var mine = UserSettings.fresh
        mine.displayUnit = .lb
        try db.save(mine)
        #expect(try db.writer.read { try db.settings($0) }.displayUnit == .lb)
    }

    @Test func theUnitIsTheExercisesOwnElseTheDisplayUnit() throws {
        let curl = exercise("Curl (Dumbbell)")
        let db = try freshDB(curl)
        #expect(try db.writer.read { try db.unit($0, for: curl.id) } == (.kg, nil))
        try db.setUnit(.lb, for: curl.id)
        try db.setRest(90, for: curl.id)
        #expect(try db.writer.read { try db.unit($0, for: curl.id) } == (.lb, 90))
        // A deleted preference no longer counts.
        try db.writer.write { try ExercisePref.filter(Column("exerciseId") == curl.id).updateAll($0, Column("deleted").set(to: true)) }
        #expect(try db.writer.read { try db.unit($0, for: curl.id) } == (.kg, nil))
    }

    @Test func bodyWeightIsTheLatestBeforeADate() throws {
        let db = try freshDB()
        try db.addBody(weightKg: 82, bodyFatPct: nil, at: now.addingTimeInterval(-30 * 86400), source: "manual")
        try db.addBody(weightKg: nil, bodyFatPct: 18, at: now.addingTimeInterval(-5 * 86400), source: "manual")
        try db.addBody(weightKg: 80, bodyFatPct: nil, at: now.addingTimeInterval(-10 * 86400), source: "manual")
        let gone = UUID()
        try db.addBody(weightKg: 75, bodyFatPct: nil, at: now, source: "manual", id: gone)
        try db.deleteBody(gone)
        try db.deleteBody(UUID())
        #expect(try db.writer.read { try db.latestBodyKg($0) } == 80)
        #expect(try db.writer.read { try db.latestBodyKg($0, before: now.addingTimeInterval(-20 * 86400)) } == 82)
        #expect(try db.writer.read { try db.latestBodyKg($0, before: now.addingTimeInterval(-60 * 86400)) } == nil)
    }

    @Test func theRunningWorkoutIsTheNewestUnfinishedOne() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        #expect(try db.writer.read { try db.runningWorkout($0) } == nil)
        try logWorkout(db, bench, [("60", 8)], daysAgo: 1, now: now)
        let older = try db.startWorkout(name: "Older", now: now.addingTimeInterval(-600))
        let newer = try db.startWorkout(name: "Newer", now: now)
        #expect(try db.writer.read { try db.runningWorkout($0) }?.id == newer.id)
        try db.deleteWorkout(newer.id)
        #expect(try db.writer.read { try db.runningWorkout($0) }?.id == older.id)
    }

    @Test func lastTimeIsTheLatestOtherFinishedWorkoutsTickedSets() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        #expect(try db.writer.read { try db.lastTime($0, exerciseId: bench.id, excluding: nil) } == nil)
        try logWorkout(db, bench, [("60", 8), ("60", 7)], daysAgo: 3, now: now)
        let latest = try logWorkout(db, bench, [("62.5", 6)], daysAgo: 1, now: now)

        let last = try #require(try db.writer.read { try db.lastTime($0, exerciseId: bench.id, excluding: nil) })
        #expect(last.1.map(\.weight) == ["62.5"])
        let before = try #require(try db.writer.read { try db.lastTime($0, exerciseId: bench.id, excluding: latest.id) })
        #expect(before.1.map(\.weight) == ["60", "60"])
        #expect(before.0 == now.addingTimeInterval(-3 * 86400))
    }

    @Test func aWorkoutsDetailHasItsExercisesInOrder() throws {
        let bench = exercise("Bench Press (Barbell)"), fly = exercise("Cable Fly")
        let db = try freshDB(bench, fly)
        let w = try db.startWorkout(name: "Push", items: [fly, bench].map { TemplateItem(exerciseId: $0.id, groupLabel: "") }, now: now)
        let detail = try #require(try db.writer.read { try db.workoutDetail($0, id: w.id) })
        #expect(detail.entries.map(\.exercise.name) == ["Cable Fly", "Bench Press (Barbell)"])
        #expect(detail.doneSetCount == 0 && detail.volumeKg == 0)
        #expect(try db.writer.read { try db.workoutDetail($0, id: UUID()) } == nil)

        // An exercise the phone doesn't have (not synced yet) is left out rather than shown blank.
        try db.writer.write { try Exercise.deleteOne($0, key: fly.id) }
        #expect(try db.writer.read { try db.workoutDetail($0, id: w.id) }?.entries.map(\.exercise.name) == ["Bench Press (Barbell)"])
    }

    @Test func finishedWorkoutsSumUpNewestFirst() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        try logWorkout(db, bench, [("60", 8), ("60", 8)], daysAgo: 2, now: now, name: "Push")
        try logWorkout(db, bench, [("70", 5)], daysAgo: 1, now: now, name: "Heavy")
        _ = try db.startWorkout(name: "Running", now: now)

        let all = try db.writer.read { try db.finishedWorkouts($0) }
        #expect(all.map(\.workout.name) == ["Heavy", "Push"])
        #expect(all[1].doneSets == 2 && all[1].volumeKg == 960 && all[1].minutes == 60)
        #expect(all[1].exerciseNames == ["Bench Press (Barbell)"] && all[1].exerciseCount == 1)
        #expect(try db.writer.read { try db.finishedWorkouts($0, limit: 1) }.map(\.workout.name) == ["Heavy"])
        #expect(WorkoutSummary(workout: try #require(try db.writer.read { try db.runningWorkout($0) }), exerciseNames: [], doneSets: 0, volumeKg: 0).minutes == nil)
    }

    @Test func templatesSayWhenTheyWereLastUsed() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let push = Template(id: UUID(), name: "Push", notes: "", items: [TemplateItem(exerciseId: bench.id, groupLabel: ""), TemplateItem(exerciseId: UUID(), groupLabel: "")], updatedAt: now, deleted: false, dirty: true)
        try db.save(push)
        #expect(try db.writer.read { try db.templates($0) }.first?.lastUsed == nil)
        try logWorkout(db, bench, [("60", 8)], daysAgo: 2, now: now, templateId: push.id)

        let summary = try #require(try db.writer.read { try db.templates($0) }.first)
        #expect(summary.lastUsed == now.addingTimeInterval(-2 * 86400))
        #expect(summary.exerciseNames == ["Bench Press (Barbell)", "Exercise"])
    }

    @Test func searchFindsByEveryWordOwnAndRecentFirst() throws {
        var press = exercise("Leg Press (Machine)")
        press.aliases = ["sled"]
        let ext = exercise("Leg Extension (Machine)"), curl = exercise("Leg Curl (Machine)")
        let mine = exercise("Leg Press Calf Raise", builtin: false), calf = exercise("Calf Raise (Machine)")
        let db = try freshDB(press, ext, curl, mine, calf)
        try logWorkout(db, curl, [("40", 12)], daysAgo: 1, now: now)

        #expect(try db.writer.read { try db.search($0, "  ") }.isEmpty)
        #expect(try db.writer.read { try db.search($0, "sled") }.map(\.exercise.name) == ["Leg Press (Machine)"])
        #expect(try db.writer.read { try db.search($0, "press leg") }.map(\.exercise.name).sorted() == ["Leg Press (Machine)", "Leg Press Calf Raise"])
        let leg = try db.writer.read { try db.search($0, "leg") }
        // Done before comes first, with what was done; then your own.
        #expect(leg.prefix(2).map(\.exercise.name) == ["Leg Curl (Machine)", "Leg Press Calf Raise"])
        #expect(leg.first?.lastText == "1 × 40 kg × 12")
        #expect(leg.count == 4)
        // A name that starts with the word beats your own that only has it later...
        #expect(try db.writer.read { try db.search($0, "calf") }.map(\.exercise.name) == ["Calf Raise (Machine)", "Leg Press Calf Raise"])
        // ...and your own beats a built-in that only has it later.
        #expect(try db.writer.read { try db.search($0, "press") }.first?.exercise.name == "Leg Press Calf Raise")
        #expect(try db.writer.read { try db.search($0, "leg", limit: 2) }.count == 2)
    }

    @Test func usualFirstsAreWhatWorkoutsUsuallyOpenWith() throws {
        let bench = exercise("Bench Press (Barbell)"), squat = exercise("Squat (Barbell)")
        let db = try freshDB(bench, squat)
        try logWorkout(db, bench, [("60", 8)], daysAgo: 3, now: now)
        try logWorkout(db, bench, [("62.5", 8)], daysAgo: 2, now: now)
        try logWorkout(db, squat, [("100", 5)], daysAgo: 1, now: now)
        let firsts = try db.writer.read { try db.usualFirsts($0) }
        #expect(firsts.map(\.exercise.name) == ["Bench Press (Barbell)", "Squat (Barbell)"])
        #expect(firsts[0].lastText == "1 × 62.5 kg × 8")
        #expect(try db.writer.read { try db.usualFirsts($0, limit: 1) }.count == 1)
    }

    @Test func historyAndCountsOfAnExercise() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        try logWorkout(db, bench, [("60", 8), ("60", 8)], daysAgo: 2, now: now, name: "First")
        try logWorkout(db, bench, [("65", 5)], daysAgo: 1, now: now, name: "Second")
        #expect(try db.writer.read { try db.history($0, exerciseId: bench.id) }.map(\.workout.name) == ["Second", "First"])
        #expect(try db.writer.read { try db.setCounts($0) } == [bench.id: 3])
        #expect(try db.writer.read { try db.exerciseRows($0) }.map(\.sets) == [3])
    }

    @Test func anEntryIsFinishedWhenEverySetIsTicked() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try db.startWorkout(name: "Push", items: [TemplateItem(exerciseId: bench.id, groupLabel: "")], now: now)
        func entry() throws -> ExerciseEntry { try #require(try db.writer.read { try db.workoutDetail($0, id: w.id) }?.entries.first) }
        #expect(try !entry().isFinished)
        try db.toggleDone(try entry().sets[0].id)
        #expect(try entry().isFinished)
        try db.deleteSet(try entry().sets[0].id)
        // No sets at all isn't finished.
        #expect(try !entry().isFinished)
    }
}

/// The user's own exercises, and how a past workout compares.
struct LibraryTests {
    @Test func usesCountWorkoutsAndTemplates() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        try logWorkout(db, bench, [("60", 8)], daysAgo: 2)
        try logWorkout(db, bench, [("60", 8)], daysAgo: 1)
        try db.save(Template(id: UUID(), name: "Push", notes: "", items: [TemplateItem(exerciseId: bench.id, groupLabel: "")], updatedAt: .now, deleted: false, dirty: true))
        try db.save(Template(id: UUID(), name: "Legs", notes: "", items: [], updatedAt: .now, deleted: false, dirty: true))
        #expect(try db.writer.read { try db.uses($0, exerciseId: bench.id) } == (2, 1))
    }

    @Test func onlyYourOwnExercisesCanBeEditedOrRemoved() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        var renamed = bench
        renamed.name = "My bench"
        try db.save(renamed)
        #expect(try db.deleteExercise(bench.id).isEmpty)
        #expect(try db.writer.read { try Exercise.fetchOne($0, key: bench.id) }?.name == "Bench Press (Barbell)")

        var mine = try db.createExercise(name: "Landmine Rotation", equipment: "barbell", measurement: .repsLoad, unilateral: true, primaryMuscles: ["obliques"])
        mine.name = "Landmine Twist"
        try db.save(mine)
        let stored = try #require(try db.writer.read { try Exercise.fetchOne($0, key: mine.id) })
        #expect(stored.name == "Landmine Twist" && stored.dirty && !stored.builtin)
        #expect(try db.writer.read { try db.myExerciseCount($0) } == 1)
        #expect(try db.deleteExercise(UUID()).isEmpty)
    }

    @Test func aBestSaysHeaviestYetAndADipSaysWhatTheTopWas() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        try logWorkout(db, bench, [("60", 8)], daysAgo: 3)
        let heavier = try logWorkout(db, bench, [("62.5", 6), ("65", 5), ("65", 4)], daysAgo: 2)
        let lighter = try logWorkout(db, bench, [("55", 10)], daysAgo: 1)

        let up = try #require(try db.writer.read { try db.workoutDetail($0, id: heavier.id) })
        let upInsight = try db.writer.read { try db.insights($0, for: up) }
        #expect(upInsight[up.entries[0].id]?.heaviestSet == up.entries[0].sets.first { $0.weight == "65" }?.id)

        let down = try #require(try db.writer.read { try db.workoutDetail($0, id: lighter.id) })
        let downInsight = try #require(try db.writer.read { try db.insights($0, for: down) }[down.entries[0].id])
        #expect(downInsight.heaviestSet == nil && downInsight.earlierTop?.weight == "65")
    }

    @Test func companionsAreExercisesDoneTogether() throws {
        let bench = exercise("Bench Press (Barbell)"), fly = exercise("Cable Fly"), squat = exercise("Squat (Barbell)")
        let db = try freshDB(bench, fly, squat)
        let w = try db.startWorkout(name: "Push", items: [bench, fly].map { TemplateItem(exerciseId: $0.id, groupLabel: "") })
        try db.writer.write { try $0.execute(sql: "UPDATE loggedSet SET done = 1") }
        try db.finishWorkout(w.id, name: "Push", notes: "")
        try db.startWorkout(name: "Legs", items: [TemplateItem(exerciseId: squat.id, groupLabel: "")])
        #expect(try db.writer.read { try db.companions($0, of: [bench.id]) }.map(\.id) == [fly.id])
        #expect(try db.writer.read { try db.companions($0, of: []) }.isEmpty)
    }

    @Test func aDeletedExerciseOfYourOwnStaysInPastWorkouts() throws {
        let db = try freshDB()
        let mine = try db.createExercise(name: "Landmine Rotation (Barbell)", equipment: "barbell", measurement: .repsLoad, unilateral: true)
        let w = try logWorkout(db, mine, [("20", 12)], daysAgo: 1)
        try db.deleteExercise(mine.id)
        #expect(try db.writer.read { try db.allExercises($0) }.isEmpty)
        #expect(try db.writer.read { try db.workoutDetail($0, id: w.id) }?.entries.first?.exercise.name == "Landmine Rotation (Barbell)")
    }

    @Test func noEarlierWorkoutMeansNothingToCompare() throws {
        let bench = exercise("Bench Press (Barbell)"), plank = exercise("Plank", .timed)
        let db = try freshDB(bench, plank)
        let w = try logWorkout(db, bench, [("60", 8)], daysAgo: 1)
        let detail = try #require(try db.writer.read { try db.workoutDetail($0, id: w.id) })
        #expect(try db.writer.read { try db.insights($0, for: detail) }.isEmpty)

        // The same top as before is neither a best nor a drop.
        let again = try logWorkout(db, bench, [("60", 8)], daysAgo: 0)
        let same = try #require(try db.writer.read { try db.workoutDetail($0, id: again.id) })
        #expect(try db.writer.read { try db.insights($0, for: same) }.isEmpty)
    }
}

/// The Progress tab's twelve weeks, bests, top exercises and body weight.
struct ProgressStoreTests {
    private let now = Date(timeIntervalSince1970: 1_794_300_000) // Tuesday 10 November 2026

    @Test func anEmptyLogHasTwelveEmptyWeeks() throws {
        let db = try freshDB()
        let data = try db.writer.read { try db.progress($0, now: now) }
        #expect(data.weeks.count == 12 && data.weeks.allSatisfy { $0.count == 0 })
        #expect(data.bests.isEmpty && data.top.isEmpty && data.body == nil && data.exerciseCount == 0)
        // Weeks start on Monday; the last one is this week.
        #expect(data.weeks.last?.start.formatted(.dateTime.weekday(.wide).day().month(.wide)) == "Monday 9 November")
    }

    @Test func weeksCountWorkoutsAndBestsAreNewHeaviests() throws {
        let bench = exercise("Bench Press (Barbell)"), squat = exercise("Squat (Barbell)"), plank = exercise("Plank", .timed)
        let db = try freshDB(bench, squat, plank)
        try logWorkout(db, bench, [("60", 8)], daysAgo: 120, now: now)   // before the twelve weeks
        try logWorkout(db, bench, [("62.5", 8)], daysAgo: 15, now: now)
        try logWorkout(db, bench, [("65", 5), ("60", 8)], daysAgo: 1, now: now)
        try logWorkout(db, squat, [("100", 5)], daysAgo: 1, now: now)
        try logWorkout(db, plank, [("", 1)], daysAgo: 2, now: now)

        let data = try db.writer.read { try db.progress($0, now: now) }
        #expect(data.weeks.map(\.count).reduce(0, +) == 4)
        // This week began on Monday: Sunday's plank was last week.
        #expect(data.weeks.suffix(2).map(\.count) == [1, 2])
        // Each exercise's latest best within the twelve weeks.
        #expect(data.bests.map(\.exercise.name) == ["Bench Press (Barbell)"])
        #expect(data.bests.first?.text == "65 kg × 5")
        #expect(data.top.first?.exercise.name == "Bench Press (Barbell)")
        #expect(data.top.first?.points == [60, 62.5, 65])
        #expect(data.top.first?.topKg == 65 && data.top.first?.sessions == 3)
        #expect(data.top.count == 3)
        #expect(data.exerciseCount == 3)
    }

    @Test func bodyWeightChangeIsSinceTheTwelveWeeksBegan() throws {
        let db = try freshDB()
        try db.addBody(weightKg: 85, bodyFatPct: nil, at: now.addingTimeInterval(-200 * 86400), source: "manual")
        let only = try db.writer.read { try db.progress($0, now: now) }.body
        #expect(only?.latestKg == 85 && only?.changeKg == nil && only?.since == nil)

        try db.addBody(weightKg: 84, bodyFatPct: nil, at: now.addingTimeInterval(-60 * 86400), source: "manual")
        try db.addBody(weightKg: 82.5, bodyFatPct: nil, at: now.addingTimeInterval(-86400), source: "manual")
        var settings = UserSettings.fresh
        settings.displayUnit = .lb
        try db.save(settings)
        let data = try db.writer.read { try db.progress($0, now: now) }
        #expect(data.body?.latestKg == 82.5)
        #expect(data.body?.changeKg == -1.5)
        #expect(data.body?.since == now.addingTimeInterval(-60 * 86400))
        #expect(data.display == .lb)
    }

    @Test func anExercisesProgressIsEverySessionNewestFirst() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        try logWorkout(db, bench, [("60", 8), ("62.5", 6)], daysAgo: 2, now: now)
        let w = try logWorkout(db, bench, [("65", 5)], daysAgo: 1, now: now)
        // A session with nothing ticked isn't one.
        let empty = try db.startWorkout(name: "Empty", items: [TemplateItem(exerciseId: bench.id, groupLabel: "")], now: now)
        try db.writer.write { try Workout.filter(Column("id") == empty.id).updateAll($0, Column("endedAt").set(to: now)) }
        try db.setRest(120, for: bench.id)
        try db.save(Template(id: UUID(), name: "Push", notes: "", items: [TemplateItem(exerciseId: bench.id, groupLabel: "")], updatedAt: now, deleted: false, dirty: true))

        let p = try #require(try db.writer.read { try db.exerciseProgress($0, id: bench.id) })
        #expect(p.sessions.map(\.workout.id).first == w.id && p.sessions.count == 2)
        #expect(p.sessions[1].topSet?.weight == "62.5")
        #expect(p.restS == 120 && p.templateCount == 1 && p.unit == .kg)
        #expect(try db.writer.read { try db.exerciseProgress($0, id: UUID()) } == nil)
    }
}

/// Everything logged, as files you keep: JSON with all of it, and a CSV with a row per ticked set.
struct ExportTests {
    @Test func theCSVHasOneRowPerTickedSetQuotedWhereNeeded() throws {
        let bench = exercise("Bench Press, \"Flat\" (Barbell)")
        let db = try freshDB(bench)
        try logWorkout(db, bench, [("60", 8), ("62.5", 6)], daysAgo: 1)
        let files = try Exporter.write(db, to: try emptyFolder())

        let lines = try String(contentsOf: files.csv, encoding: .utf8).split(separator: "\n")
        #expect(lines.first == "date,workout,exercise,set,weight,unit,reps,seconds,metres,each_side")
        #expect(lines.count == 3)
        #expect(lines[1].contains("\"Bench Press, \"\"Flat\"\" (Barbell)\",1,60,kg,8"))
        let json = try #require(try JSONSerialization.jsonObject(with: Data(contentsOf: files.json)) as? [String: Any])
        #expect((json["workouts"] as? [[String: Any]])?.count == 1 && json["display_unit"] as? String == "kg")
    }

    @Test func everythingYouLoggedIsInTheExport() throws {
        let bench = exercise("Bench Press (Barbell)"), plank = exercise("Plank", .timed), row = exercise("Row", .distance)
        let db = try freshDB(bench, plank, row)
        let mine = try db.createExercise(name: "Landmine Twist", equipment: "barbell", measurement: .repsLoad, unilateral: true, primaryMuscles: ["obliques"])
        let w = try db.startWorkout(name: "Mixed", items: [TemplateItem(exerciseId: plank.id, groupLabel: "a"), TemplateItem(exerciseId: row.id, groupLabel: "a")])
        let detail = try #require(try db.writer.read { try db.workoutDetail($0, id: w.id) })
        var hold = detail.entries[0].sets[0]
        hold.durationS = 60; hold.done = true
        try db.update(hold)
        var rowed = detail.entries[1].sets[0]
        rowed.distanceM = 500; rowed.durationS = 110; rowed.perSide = true; rowed.done = true
        try db.update(rowed)
        try db.finishWorkout(w.id, name: "Mixed", notes: "")
        try db.save(Template(id: UUID(), name: "Core", notes: "", items: [TemplateItem(exerciseId: plank.id, groupLabel: ""), TemplateItem(exerciseId: UUID(), groupLabel: "")], updatedAt: .now, deleted: false, dirty: true))
        try db.addBody(weightKg: 80, bodyFatPct: 18, at: .now, source: "manual")

        let files = try Exporter.write(db, to: try emptyFolder())

        let csv = try String(contentsOf: files.csv, encoding: .utf8).split(separator: "\n").map(String.init)
        #expect(csv.count == 3)
        #expect(csv[1].hasSuffix(",Mixed,Plank,1,,,,60,,"))
        #expect(csv[2].hasSuffix(",Mixed,Row,1,,,,110,500,yes"))
        let json = try #require(try JSONSerialization.jsonObject(with: Data(contentsOf: files.json)) as? [String: Any])
        let templates = try #require(json["templates"] as? [[String: Any]])
        #expect(templates.first?["exercises"] as? [String] == ["Plank", "Exercise"])
        #expect((json["body"] as? [[String: Any]])?.first?["body_fat_pct"] as? Double == 18)
        let yours = try #require(json["your_exercises"] as? [[String: Any]])
        #expect(yours.map { $0["name"] as? String } == [mine.name])
        let entries = try #require((json["workouts"] as? [[String: Any]])?.first?["exercises"] as? [[String: Any]])
        #expect(entries.map { $0["superset"] as? String } == ["a", "a"])
    }

    private func emptyFolder() throws -> URL {
        let dir = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }
}
