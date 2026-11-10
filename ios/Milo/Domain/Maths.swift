import Foundation

// Milo's numbers, matching the server exactly. testdata/maths (repo root) pins
// every result; MiloTests runs the same vectors as the Go tests.

nonisolated enum WeightUnit: String, Codable, CaseIterable, Sendable {
    case kg, lb
}

nonisolated enum MeasurementKind: String, Codable, CaseIterable, Sendable {
    case repsLoad = "reps_load"
    case bodyweight
    case assisted
    case timed
    case distance
}

/// What kind of set it was. A warm-up is logged but not counted: it adds nothing
/// to volume and is never a top set or a best. The others are working sets.
nonisolated enum SetKind: String, Codable, CaseIterable, Sendable {
    case normal, warmup, drop, failure
}

nonisolated enum Maths {
    static let kgPerLb = 0.45359237
    static let defaultMET = 5.0

    /// Kilograms for an exact typed weight; nil when empty or unreadable.
    static func toKg(_ weight: String, _ unit: WeightUnit) -> Double? {
        guard !weight.isEmpty, let v = Double(weight) else { return nil }
        return unit == .lb ? v * kgPerLb : v
    }

    /// A stored weight shown in another unit, rounded to 0.1 with ".0" dropped.
    /// The stored weight never changes.
    static func displayWeight(_ weight: String, _ unit: WeightUnit, in display: WeightUnit) -> String {
        guard let kg = toKg(weight, unit) else { return "" }
        return tenths(display == .lb ? kg / kgPerLb : kg)
    }

    /// Rounds half away from zero to one decimal and drops a trailing ".0".
    static func tenths(_ v: Double) -> String {
        let r = (v * 10).rounded(.toNearestOrAwayFromZero) / 10
        let s = String(format: "%.1f", r)
        return s.hasSuffix(".0") ? String(s.dropLast(2)) : s
    }

    struct SetInput: Sendable, Decodable {
        var measurement: MeasurementKind
        var weight: String
        var unit: WeightUnit
        var reps: Int
        var perSide: Bool
        var done: Bool
        var kind: SetKind

        /// Done, and not a warm-up.
        var isWorking: Bool { done && kind != .warmup }

        enum CodingKeys: String, CodingKey {
            case measurement, weight, unit, reps, done, kind
            case perSide = "per_side"
        }

        init(measurement: MeasurementKind, weight: String, unit: WeightUnit, reps: Int, perSide: Bool, done: Bool, kind: SetKind = .normal) {
            (self.measurement, self.weight, self.unit, self.reps, self.perSide, self.done, self.kind) = (measurement, weight, unit, reps, perSide, done, kind)
        }

        /// The shared test vectors leave `kind` out for an ordinary set.
        init(from decoder: any Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            self.init(
                measurement: try c.decode(MeasurementKind.self, forKey: .measurement), weight: try c.decode(String.self, forKey: .weight),
                unit: try c.decode(WeightUnit.self, forKey: .unit), reps: try c.decode(Int.self, forKey: .reps),
                perSide: try c.decode(Bool.self, forKey: .perSide), done: try c.decode(Bool.self, forKey: .done),
                kind: try c.decodeIfPresent(SetKind.self, forKey: .kind) ?? .normal)
        }
    }

    static func setVolumeKg(_ s: SetInput) -> Double {
        guard s.isWorking, s.measurement == .repsLoad, s.reps > 0, let kg = toKg(s.weight, s.unit) else { return 0 }
        return kg * Double(s.reps) * (s.perSide ? 2 : 1)
    }

    static func volumeKg(_ sets: [SetInput]) -> Double {
        sets.reduce(0) { $0 + setVolumeKg($1) }
    }

    /// What was actually moved. bodyKg 0 means unknown.
    static func effectiveLoadKg(_ m: MeasurementKind, weight: String, unit: WeightUnit, bodyKg: Double) -> Double? {
        let kg = toKg(weight, unit)
        switch m {
        case .repsLoad:
            return kg
        case .bodyweight:
            guard bodyKg > 0 else { return nil }
            return bodyKg + (kg ?? 0)
        case .assisted:
            guard bodyKg > 0 else { return nil }
            return max(0, bodyKg - (kg ?? 0))
        case .timed, .distance:
            return nil
        }
    }

    /// Epley, for 1 to 12 reps.
    static func e1rm(loadKg: Double, reps: Int) -> Double? {
        guard loadKg > 0, (1...12).contains(reps) else { return nil }
        return reps == 1 ? loadKg : loadKg * (1 + Double(reps) / 30)
    }

    struct Summary: Equatable, Sendable {
        var volumeKg: Double = 0
        var topLoadKg: Double?
        var topReps: Int?
        var e1rmKg: Double?
    }

    static func summarize(_ sets: [SetInput], bodyKg: Double) -> Summary {
        var s = Summary(volumeKg: volumeKg(sets))
        for set in sets where set.isWorking && set.reps > 0 {
            guard let load = effectiveLoadKg(set.measurement, weight: set.weight, unit: set.unit, bodyKg: bodyKg) else { continue }
            if let top = s.topLoadKg, let reps = s.topReps {
                if load > top || (load == top && set.reps > reps) {
                    s.topLoadKg = load
                    s.topReps = set.reps
                }
            } else {
                s.topLoadKg = load
                s.topReps = set.reps
            }
            if let e = e1rm(loadKg: load, reps: set.reps), e > (s.e1rmKg ?? -1) {
                s.e1rmKg = e
            }
        }
        return s
    }

    struct ExerciseShare: Sendable, Decodable {
        var met: Double?
        var doneSets: Int

        enum CodingKeys: String, CodingKey {
            case met
            case doneSets = "done_sets"
        }
    }

    /// (MET − 1) × body kg × hours × share of done sets, summed over exercises.
    static func activeKcal(durationS: Double, bodyKg: Double, exercises: [ExerciseShare]) -> Double? {
        let total = exercises.reduce(0) { $0 + $1.doneSets }
        guard bodyKg > 0, durationS > 0, total > 0 else { return nil }
        let hours = durationS / 3600
        return exercises.reduce(0) { acc, e in
            acc + ((e.met ?? defaultMET) - 1) * bodyKg * hours * Double(e.doneSets) / Double(total)
        }
    }

    struct Session: Sendable, Decodable {
        var date: String
        var sets: [SetInput]
    }

    struct Best: Equatable, Sendable {
        enum Kind: String { case heaviest, e1rm }
        var date: String
        var kind: Kind
        var value: Double
        var reps: Int?
    }

    /// Sessions oldest first; a session is a best when it beats every earlier one. The first is the baseline.
    static func newBests(_ sessions: [Session], bodyKg: Double) -> [Best] {
        var out: [Best] = []
        var top: Double?
        var bestE1: Double?
        let eps = 1e-9
        for session in sessions {
            let s = summarize(session.sets, bodyKg: bodyKg)
            if let load = s.topLoadKg {
                if let t = top, load > t + eps {
                    out.append(Best(date: session.date, kind: .heaviest, value: load, reps: s.topReps))
                }
                top = max(top ?? load, load)
            }
            if let e = s.e1rmKg {
                if let b = bestE1, e > b + eps {
                    out.append(Best(date: session.date, kind: .e1rm, value: e, reps: nil))
                }
                bestE1 = max(bestE1 ?? e, e)
            }
        }
        return out
    }
}
