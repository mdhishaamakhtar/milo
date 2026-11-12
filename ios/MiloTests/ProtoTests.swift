import Foundation
import GRDB
import MiloAPI
import SwiftProtobuf
import Testing
@testable import Milo

/// Every row survives the trip to the server and back: what the app pushes is what it
/// reads when the row comes down again. A field missed in Proto.swift fails here.
struct ProtoRoundTripTests {
    private let at = Date(timeIntervalSince1970: 1_794_300_000.25)

    /// The record after going out as a change and coming back, as it would from the server.
    private func roundTrip<R: SyncedRecord & Equatable>(_ record: R) throws -> R {
        let wire = try Milo_Change(serializedBytes: record.change.serializedData())
        return try #require(Proto.record(wire) as? R)
    }

    @Test func exercises() throws {
        let full = Exercise(id: UUID(), builtin: false, name: "Landmine Press (Barbell)", aliases: ["landmine"], primaryMuscles: ["front delts"], secondaryMuscles: ["triceps"], movementPattern: "press", equipment: "barbell", measurement: .repsLoad, unilateral: true, met: 4.5, about: "One arm.", approximate: true, updatedAt: at, deleted: false, dirty: false)
        #expect(try roundTrip(full) == full)
        for m in MeasurementKind.allCases {
            var e = full
            e.measurement = m; e.met = nil; e.deleted = true
            #expect(try roundTrip(e) == e)
        }
    }

    @Test func workoutsWithAndWithoutTheirOptionalParts() throws {
        let running = Workout(id: UUID(), name: "Push", notes: "", startedAt: at, endedAt: nil, templateId: nil, healthUUID: nil, updatedAt: at, deleted: false, dirty: false)
        let finished = Workout(id: UUID(), name: "Push", notes: "Good", startedAt: at, endedAt: at.addingTimeInterval(3600), templateId: UUID(), healthUUID: UUID(), updatedAt: at, deleted: true, dirty: false)
        #expect(try roundTrip(running) == running)
        #expect(try roundTrip(finished) == finished)
    }

    @Test func setsOfEveryKind() throws {
        let base = LoggedSet(id: UUID(), workoutExerciseId: UUID(), position: 3, kind: .normal, reps: nil, weight: "", unit: .kg, perSide: false, durationS: nil, distanceM: nil, rpe: nil, done: false, updatedAt: at, deleted: false, dirty: false)
        #expect(try roundTrip(base) == base)
        for kind in SetKind.allCases {
            var s = base
            s.kind = kind; s.reps = 8; s.weight = "62.5"; s.unit = .lb; s.perSide = true
            s.durationS = 45; s.distanceM = 400; s.rpe = 8.5; s.done = true
            #expect(try roundTrip(s) == s)
        }
    }

    @Test func theOtherRows() throws {
        let we = WorkoutExercise(id: UUID(), workoutId: UUID(), exerciseId: UUID(), position: 2, groupLabel: "a", notes: "Seat 4", updatedAt: at, deleted: false, dirty: false)
        #expect(try roundTrip(we) == we)
        let pref = ExercisePref(exerciseId: UUID(), unit: .lb, restS: 90, updatedAt: at, deleted: false, dirty: false)
        #expect(try roundTrip(pref) == pref)
        let noRest = ExercisePref(exerciseId: UUID(), unit: .kg, restS: nil, updatedAt: at, deleted: true, dirty: false)
        #expect(try roundTrip(noRest) == noRest)
        let template = Template(id: UUID(), name: "Pull", notes: "Heavy", items: [TemplateItem(exerciseId: UUID(), groupLabel: ""), TemplateItem(exerciseId: UUID(), groupLabel: "s")], updatedAt: at, deleted: false, dirty: false)
        #expect(try roundTrip(template) == template)
        let weight = BodyMeasurement(id: UUID(), measuredAt: at, weightKg: 81.2, bodyFatPct: nil, source: "health", updatedAt: at, deleted: false, dirty: false)
        #expect(try roundTrip(weight) == weight)
        let fat = BodyMeasurement(id: UUID(), measuredAt: at, weightKg: nil, bodyFatPct: 18.5, source: "manual", updatedAt: at, deleted: true, dirty: false)
        #expect(try roundTrip(fat) == fat)
        let settings = UserSettings(displayUnit: .lb, restDefaultS: 120, heightCm: 178, birthDate: "1995-04-02", sex: "male", updatedAt: at, dirty: false)
        #expect(try roundTrip(settings) == settings)
        let fresh = UserSettings(displayUnit: .kg, restDefaultS: 60, heightCm: nil, birthDate: "", sex: "", updatedAt: at, dirty: false)
        #expect(try roundTrip(fresh) == fresh)
    }

    @Test func rowsWithoutAnIdAreNotRows() {
        var c = Milo_Change()
        #expect(Proto.record(c) == nil)
        c.exercise = Milo_Exercise(); #expect(Proto.record(c) == nil)
        c.exercisePref = Milo_ExercisePref(); #expect(Proto.record(c) == nil)
        c.workout = Milo_Workout(); #expect(Proto.record(c) == nil)
        c.workoutExercise = Milo_WorkoutExercise(); #expect(Proto.record(c) == nil)
        c.set = Milo_Set(); #expect(Proto.record(c) == nil)
        c.template = Milo_Template(); #expect(Proto.record(c) == nil)
        c.bodyMeasurement = Milo_BodyMeasurement(); #expect(Proto.record(c) == nil)
        // A template item without an exercise is dropped, not the template.
        var t = Milo_Template()
        t.id = Proto.id(UUID())
        t.items = [Milo_TemplateItem()]
        c.template = t
        #expect((Proto.record(c) as? Template)?.items.isEmpty == true)
    }

    @Test func unknownEnumValuesFallBackToTheOrdinaryOne() {
        #expect(MeasurementKind(Milo_Measurement.unspecified) == .repsLoad)
        #expect(SetKind(Milo_SetKind.unspecified) == .normal)
        #expect(WeightUnit(Milo_WeightUnit.unspecified) == .kg)
        // The server's ids are lower case.
        #expect(Proto.id(UUID(uuidString: "6F9619FF-8B86-D011-B42D-00CF4FC964FF")!) == "6f9619ff-8b86-d011-b42d-00cf4fc964ff")
    }
}

/// The table and key a card's base versions name, and what the store calls each row.
struct RecordTests {
    @Test func everyTableIsFoundByItsName() throws {
        let db = try AppDatabase.inMemory()
        let at = Date(timeIntervalSince1970: 1_794_300_000)
        let exercise = Exercise(id: UUID(), builtin: false, name: "Curl", aliases: [], primaryMuscles: [], secondaryMuscles: [], movementPattern: "", equipment: "", measurement: .repsLoad, unilateral: false, met: nil, about: "", approximate: false, updatedAt: at, deleted: false, dirty: false)
        let rows: [any SyncedRecord] = [
            exercise,
            ExercisePref(exerciseId: exercise.id, unit: .lb, restS: nil, updatedAt: at, deleted: false, dirty: false),
            Workout(id: UUID(), name: "W", notes: "", startedAt: at, endedAt: nil, templateId: nil, healthUUID: nil, updatedAt: at, deleted: false, dirty: false),
            WorkoutExercise(id: UUID(), workoutId: UUID(), exerciseId: exercise.id, position: 1, groupLabel: "", notes: "", updatedAt: at, deleted: false, dirty: false),
            LoggedSet(id: UUID(), workoutExerciseId: UUID(), position: 1, kind: .normal, reps: 5, weight: "100", unit: .kg, perSide: false, durationS: nil, distanceM: nil, rpe: nil, done: true, updatedAt: at, deleted: false, dirty: false),
            Template(id: UUID(), name: "T", notes: "", items: [], updatedAt: at, deleted: false, dirty: false),
            BodyMeasurement(id: UUID(), measuredAt: at, weightKg: 80, bodyFatPct: nil, source: "manual", updatedAt: at, deleted: false, dirty: false),
            UserSettings(displayUnit: .lb, restDefaultS: 90, heightCm: nil, birthDate: "", sex: "", updatedAt: at, dirty: false),
        ]
        try db.writer.write { d in for r in rows { try r.save(d) } }
        try db.writer.read { d in
            for r in rows {
                let found = try Synced.stored(d, table: type(of: r).syncTable, key: r.syncKey)
                #expect(found?.syncKey == r.syncKey)
            }
            #expect(try Synced.stored(d, table: "nonsense", key: UUID()) == nil)
        }
        #expect(Set(rows.map { type(of: $0).syncTable }) == ["exercise", "exercise_pref", "workout", "workout_exercise", "set", "template", "body_measurement", "settings"])
    }

    @Test func anExercisesNameSplitsIntoNameAndEquipment() {
        func named(_ name: String, equipment: String = "") -> Exercise {
            Exercise(id: UUID(), builtin: true, name: name, aliases: [], primaryMuscles: [], secondaryMuscles: [], movementPattern: "", equipment: equipment, measurement: .repsLoad, unilateral: false, met: nil, about: "", approximate: false, updatedAt: .now, deleted: false, dirty: false)
        }
        #expect(named("Leg Extension (Machine)").shortName == "Leg Extension")
        #expect(named("Leg Extension (Machine)").equipmentLabel == "Machine")
        #expect(named("Pull Up", equipment: "bodyweight").shortName == "Pull Up")
        #expect(named("Pull Up", equipment: "bodyweight").equipmentLabel == "Bodyweight")
        // A bracket that closes before it opens isn't equipment.
        #expect(named("Odd) (Name", equipment: "cable").equipmentLabel == "Cable")
    }

    @Test func warmUpsAndUntickedSetsDontCount() {
        func set(_ kind: SetKind, done: Bool) -> LoggedSet {
            LoggedSet(id: UUID(), workoutExerciseId: UUID(), position: 1, kind: kind, reps: 5, weight: "100", unit: .kg, perSide: false, durationS: nil, distanceM: nil, rpe: nil, done: done, updatedAt: .now, deleted: false, dirty: false)
        }
        #expect(set(.normal, done: true).isWorking)
        #expect(set(.drop, done: true).isWorking)
        #expect(!set(.warmup, done: true).isWorking)
        #expect(!set(.normal, done: false).isWorking)
    }
}
