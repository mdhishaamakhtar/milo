import Foundation
import Testing
@testable import Milo

/// Numbers and dates the way a lifter says them. Tests run in en-GB, Europe/London.
struct FormatTests {
    private func set(_ weight: String = "", _ unit: WeightUnit = .kg, reps: Int? = nil, seconds: Int? = nil, metres: Double? = nil, perSide: Bool = false, done: Bool = true, kind: SetKind = .normal) -> LoggedSet {
        LoggedSet(id: UUID(), workoutExerciseId: UUID(), position: 1, kind: kind, reps: reps, weight: weight, unit: unit, perSide: perSide, durationS: seconds, distanceM: metres, rpe: nil, done: done, updatedAt: .now, deleted: false, dirty: false)
    }

    @Test func aSetReadsTheWayItWasDone() {
        #expect(Format.set(set("60", reps: 8), .repsLoad) == "60 kg × 8")
        #expect(Format.set(set("20", reps: 12, perSide: true), .repsLoad) == "20 kg × 12 each side")
        #expect(Format.set(set(reps: 15), .repsLoad) == "15 reps")
        #expect(Format.set(set(), .repsLoad) == "– reps")
        #expect(Format.set(set("25", reps: 10), .assisted) == "25 kg × 10")
        #expect(Format.set(set(reps: 12), .bodyweight) == "12 reps")
        #expect(Format.set(set("10", reps: 8), .bodyweight) == "+10 kg × 8")
        #expect(Format.set(set(), .bodyweight) == "0 reps")
        #expect(Format.set(set(seconds: 45), .timed) == "45 s")
        #expect(Format.set(set("16", seconds: 90), .timed) == "1:30 at 16 kg")
        #expect(Format.set(set(), .timed) == "–")
        #expect(Format.set(set(seconds: 300, metres: 1500), .distance) == "1.5 km in 5 min")
        #expect(Format.set(set(metres: 400), .distance) == "400 m")
        #expect(Format.set(set(), .distance) == "–")
    }

    @Test func aSetShownInAnotherUnitIsConvertedNotChanged() {
        let lb = set("45", .lb, reps: 10)
        #expect(Format.set(lb, .repsLoad, unit: .kg) == "20.4 kg × 10")
        #expect(Format.set(lb, .repsLoad, unit: .kg, withUnit: false) == "20.4 × 10")
        #expect(Format.weight(lb, in: .lb) == "45")
        #expect(Format.weight(set("60.50", reps: 1), in: .kg) == "60.5")
        #expect(Format.weight(set(), in: .lb) == "")
    }

    @Test func trailingZerosGoOnlyAfterAPoint() {
        #expect(Format.trim("60.50") == "60.5")
        #expect(Format.trim("60.0") == "60")
        #expect(Format.trim("100") == "100")
    }

    @Test func aRunOfSetsCollapses() {
        #expect(Format.setsLine([], .repsLoad) == "")
        #expect(Format.setsLine(Array(repeating: set("60", reps: 8), count: 3), .repsLoad) == "3 × 60 kg × 8")
        #expect(Format.setsLine(Array(repeating: set(reps: 12), count: 3), .bodyweight) == "3 × 12")
        #expect(Format.setsLine([set("30", reps: 15), set("35", reps: 15), set("40", reps: 15)], .repsLoad) == "30, 35, 40 kg × 15")
        #expect(Format.setsLine([set("60", reps: 8), set("60", reps: 6)], .repsLoad) == "60 kg × 8, 60 kg × 6")
        // Mixed units don't share one unit at the end.
        #expect(Format.setsLine([set("20", reps: 10), set("45", .lb, reps: 10)], .repsLoad) == "20 kg × 10, 45 lb × 10")
    }

    private func entry(_ m: MeasurementKind, _ sets: [LoggedSet], unit: WeightUnit = .kg) -> ExerciseEntry {
        let e = Exercise(id: UUID(), builtin: true, name: "Lift", aliases: [], primaryMuscles: [], secondaryMuscles: [], movementPattern: "", equipment: "", measurement: m, unilateral: false, met: nil, about: "", approximate: false, updatedAt: .now, deleted: false, dirty: false)
        return ExerciseEntry(we: WorkoutExercise(id: UUID(), workoutId: UUID(), exerciseId: e.id, position: 1, groupLabel: "", notes: "", updatedAt: .now, deleted: false, dirty: false), exercise: e, sets: sets, unit: unit, restS: nil, lastTime: [], lastDate: nil)
    }

    @Test func anExerciseSumsUpInOneLine() {
        #expect(Format.summary(entry(.repsLoad, [set("60", reps: 8, done: false)])) == "No sets yet")
        #expect(Format.summary(entry(.repsLoad, Array(repeating: set("60", reps: 8), count: 3))) == "3 × 8 at 60 kg")
        #expect(Format.summary(entry(.repsLoad, Array(repeating: set("45", .lb, reps: 8), count: 2), unit: .kg)) == "2 × 8 at 20.4 kg")
        #expect(Format.summary(entry(.bodyweight, Array(repeating: set(reps: 12), count: 3))) == "3 × 12")
        #expect(Format.summary(entry(.timed, Array(repeating: set(seconds: 45), count: 2))) == "2 × 45 s")
        #expect(Format.summary(entry(.repsLoad, [set("20", reps: 10, kind: .warmup), set("60", reps: 8), set("65", reps: 5)])) == "3 sets, top 65 kg")
        #expect(Format.summary(entry(.bodyweight, [set(reps: 12), set(reps: 10)])) == "2 sets")
    }

    @Test func timesAndDistances() {
        #expect(Format.duration(59) == "59 s")
        #expect(Format.duration(120) == "2 min")
        #expect(Format.duration(95) == "1:35")
        #expect(Format.clock(59) == "0:59")
        #expect(Format.clock(3725) == "1:02:05")
        #expect(Format.distance(5000) == "5 km")
        #expect(Format.distance(999.6) == "1000 m")
    }

    @Test func massesInTheDisplayUnit() {
        #expect(Format.mass(2142.4, .kg) == "2,142 kg")
        #expect(Format.mass(100, .lb) == "220 lb")
        #expect(Format.kgIn(100, .lb) == "220.5")
        #expect(Format.kgIn(82.46, .kg) == "82.5")
    }

    @Test func daysAroundToday() {
        let now = Date(timeIntervalSince1970: 1_794_300_000) // Tuesday 10 November 2026, 08:40
        let day: TimeInterval = 86400
        #expect(Format.relativeDay(now.addingTimeInterval(-3600), now: now) == "today")
        #expect(Format.relativeDay(now.addingTimeInterval(-day), now: now) == "yesterday")
        #expect(Format.relativeDay(now.addingTimeInterval(-35 * day), now: now) == "6 Oct")
        #expect(Format.day(now.addingTimeInterval(-35 * day), now: now) == "Tue 6 Oct")
        #expect(Format.day(now.addingTimeInterval(-400 * day), now: now) == "6 Oct 2025")
        #expect(Format.longDay(now) == "Tuesday 10 November")
    }

    @Test func musclesInPlainWords() {
        #expect(Format.muscles(["front delts", "triceps", "side delts", "rear delts"]) == "Front shoulders, triceps, side shoulders, rear shoulders")
        #expect(Format.muscles(["chest"]) == "Chest")
        #expect(Format.muscles([]) == "")
    }

}

/// What stands beside exercises and sets: superset letters and set marks.
struct LabelTests {
    @Test func neighboursInAGroupAreLettered() {
        #expect(Superset.labels(["", "", "g", "g"]) == ["1", "2", "3a", "3b"])
        #expect(Superset.labels(["a", "a", "a", ""]) == ["1a", "1b", "1c", "2"])
        // Not neighbours, so not a superset.
        #expect(Superset.labels(["g", "", "g"]) == ["1", "2", "3"])
        #expect(Superset.labels([]) == [])
    }

    @Test func onlyOrdinarySetsTakeANumber() {
        #expect(SetMark.marks([.warmup, .normal, .normal, .drop, .normal, .failure]).map(\.text) == ["W", "1", "2", "D", "3", "F"])
        #expect(SetKind.allCases.map(\.title) == ["Normal set", "Warm-up", "Drop set", "To failure"])
    }
}
