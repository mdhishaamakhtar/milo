import Foundation
import Testing
@testable import Milo

/// The same vectors the Go server's tests read, from testdata/maths at the repo root.
private final class BundleToken {}

private struct VectorCase<In: Decodable, Want: Decodable>: Decodable {
    var name: String
    var input: In
    var expected: Want
}

private func load<In: Decodable, Want: Decodable>(_ file: String, _: In.Type, _: Want.Type) throws -> [(String, In, Want)] {
    let dir = try #require(Bundle(for: BundleToken.self).url(forResource: "testdata", withExtension: nil))
    let data = try Data(contentsOf: dir.appending(path: "maths/\(file)"))
    let cases = try JSONDecoder().decode([VectorCase<In, Want>].self, from: data)
    #expect(!cases.isEmpty)
    return cases.map { ($0.name, $0.input, $0.expected) }
}

private func near(_ a: Double?, _ b: Double?) -> Bool {
    switch (a, b) {
    case (nil, nil): true
    case let (a?, b?): abs(a - b) < 0.005
    default: false
    }
}

struct MathsVectorTests {
    @Test func units() throws {
        struct In: Decodable { var op: String; var weight: String; var unit: WeightUnit; var display_unit: WeightUnit? }
        struct Want: Decodable { var kg: Double?; var text: String? }
        for (name, i, w) in try load("units.json", In.self, Want.self) {
            if i.op == "to_kg" {
                #expect(near(Maths.toKg(i.weight, i.unit), w.kg), "\(name)")
            } else {
                #expect(Maths.displayWeight(i.weight, i.unit, in: i.display_unit!) == w.text, "\(name)")
            }
        }
    }

    @Test func volume() throws {
        struct In: Decodable { var sets: [Maths.SetInput] }
        struct Want: Decodable { var volume_kg: Double }
        for (name, i, w) in try load("volume.json", In.self, Want.self) {
            #expect(near(Maths.volumeKg(i.sets), w.volume_kg), "\(name)")
        }
    }

    @Test func e1rm() throws {
        struct In: Decodable { var load_kg: Double; var reps: Int }
        struct Want: Decodable { var e1rm_kg: Double? }
        for (name, i, w) in try load("e1rm.json", In.self, Want.self) {
            #expect(near(Maths.e1rm(loadKg: i.load_kg, reps: i.reps), w.e1rm_kg), "\(name)")
        }
    }

    @Test func effectiveLoad() throws {
        struct In: Decodable { var measurement: MeasurementKind; var weight: String; var unit: WeightUnit; var body_kg: Double }
        struct Want: Decodable { var load_kg: Double? }
        for (name, i, w) in try load("effective_load.json", In.self, Want.self) {
            #expect(near(Maths.effectiveLoadKg(i.measurement, weight: i.weight, unit: i.unit, bodyKg: i.body_kg), w.load_kg), "\(name)")
        }
    }

    @Test func calories() throws {
        struct In: Decodable { var duration_s: Double; var body_kg: Double; var exercises: [Maths.ExerciseShare] }
        struct Want: Decodable { var kcal: Double? }
        for (name, i, w) in try load("calories.json", In.self, Want.self) {
            #expect(near(Maths.activeKcal(durationS: i.duration_s, bodyKg: i.body_kg, exercises: i.exercises), w.kcal), "\(name)")
        }
    }

    @Test func progress() throws {
        struct In: Decodable { var sets: [Maths.SetInput]; var body_kg: Double }
        struct Want: Decodable { var volume_kg: Double; var top_load_kg: Double?; var top_reps: Int?; var e1rm_kg: Double? }
        for (name, i, w) in try load("progress.json", In.self, Want.self) {
            let s = Maths.summarize(i.sets, bodyKg: i.body_kg)
            #expect(near(s.volumeKg, w.volume_kg), "\(name) volume")
            #expect(near(s.topLoadKg, w.top_load_kg), "\(name) top")
            #expect(s.topReps == w.top_reps, "\(name) reps")
            #expect(near(s.e1rmKg, w.e1rm_kg), "\(name) e1rm")
        }
    }

    @Test func bests() throws {
        struct In: Decodable { var sessions: [Maths.Session]; var body_kg: Double }
        struct B: Decodable { var date: String; var kind: String; var value: Double; var reps: Int? }
        struct Want: Decodable { var bests: [B] }
        for (name, i, w) in try load("bests.json", In.self, Want.self) {
            let got = Maths.newBests(i.sessions, bodyKg: i.body_kg)
            #expect(got.count == w.bests.count, "\(name)")
            for (g, b) in zip(got, w.bests) {
                #expect(g.date == b.date && g.kind.rawValue == b.kind && near(g.value, b.value) && g.reps == b.reps, "\(name)")
            }
        }
    }
}
