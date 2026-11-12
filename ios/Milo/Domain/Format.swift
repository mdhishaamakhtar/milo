import Foundation

/// How Milo writes numbers and dates: the way a lifter says them.
nonisolated enum Format {
    static func trim(_ s: String) -> String {
        guard s.contains(".") else { return s }
        var t = s
        while t.hasSuffix("0") { t.removeLast() }
        if t.hasSuffix(".") { t.removeLast() }
        return t
    }

    /// A set's weight shown in `display`, converted only when the units differ.
    static func weight(_ set: LoggedSet, in display: WeightUnit) -> String {
        guard !set.weight.isEmpty else { return "" }
        return set.unit == display ? trim(set.weight) : Maths.displayWeight(set.weight, set.unit, in: display)
    }

    /// "20 kg × 15", "15 reps", "45 s", "20 kg × 12 each side".
    static func set(_ s: LoggedSet, _ m: MeasurementKind, unit: WeightUnit? = nil, withUnit: Bool = true) -> String {
        let shown = unit ?? s.unit
        let w = weight(s, in: shown)
        let wText = w.isEmpty ? "" : (withUnit ? "\(w) \(shown.rawValue)" : w)
        var text: String
        switch m {
        case .timed:
            text = s.durationS.map(duration) ?? "–"
            if !wText.isEmpty { text += " at \(wText)" }
        case .distance:
            text = s.distanceM.map(distance) ?? "–"
            if let d = s.durationS { text += " in \(duration(d))" }
        case .bodyweight:
            let r = s.reps ?? 0
            text = wText.isEmpty ? "\(r) reps" : "+\(wText) × \(r)"
        case .repsLoad, .assisted:
            let r = s.reps.map(String.init) ?? "–"
            text = wText.isEmpty ? "\(r) reps" : "\(wText) × \(r)"
        }
        if s.perSide { text += " each side" }
        return text
    }

    /// "Last 7 Nov: 30, 35, 40 kg × 15" style summaries collapse to "30 × 15, 35 × 15".
    static func setsLine(_ sets: [LoggedSet], _ m: MeasurementKind) -> String {
        guard let first = sets.first else { return "" }
        let texts = sets.map { set($0, m) }
        if Set(texts).count == 1 { return "\(sets.count) × " + set(first, m).replacingOccurrences(of: "\(first.reps ?? 0) reps", with: "\(first.reps ?? 0)") }
        if m == .repsLoad, Set(sets.map(\.reps)).count == 1, let reps = first.reps, Set(sets.map(\.unit)).count == 1 {
            return sets.map { trim($0.weight) }.joined(separator: ", ") + " \(first.unit.rawValue) × \(reps)"
        }
        return texts.joined(separator: ", ")
    }

    /// "3 × 15", "3 sets, top 45 lb", "35, 40, 40 kg × 15".
    static func summary(_ entry: ExerciseEntry) -> String {
        let done = entry.doneSets
        guard !done.isEmpty else { return "No sets yet" }
        let m = entry.exercise.measurement
        if Set(done.map { set($0, m) }).count == 1 {
            let s = done[0]
            if m == .repsLoad || m == .assisted, !s.weight.isEmpty { return "\(done.count) × \(s.reps ?? 0) at \(weight(s, in: entry.unit)) \(entry.unit.rawValue)" }
            if m == .bodyweight, s.weight.isEmpty { return "\(done.count) × \(s.reps ?? 0)" }
            return "\(done.count) × " + set(s, m)
        }
        let top = done.filter(\.isWorking).max { (Maths.toKg($0.weight, $0.unit) ?? 0) < (Maths.toKg($1.weight, $1.unit) ?? 0) }
        if let top, !top.weight.isEmpty {
            return "\(done.count) sets, top \(weight(top, in: entry.unit)) \(entry.unit.rawValue)"
        }
        return "\(done.count) sets"
    }

    static func duration(_ s: Int) -> String {
        if s < 60 { return "\(s) s" }
        return s % 60 == 0 ? "\(s / 60) min" : String(format: "%d:%02d", s / 60, s % 60)
    }

    static func distance(_ m: Double) -> String {
        m >= 1000 ? "\(trim(String(format: "%.2f", m / 1000))) km" : "\(Int(m.rounded())) m"
    }

    static func clock(_ seconds: Int) -> String {
        let h = seconds / 3600, m = (seconds % 3600) / 60, s = seconds % 60
        return h > 0 ? String(format: "%d:%02d:%02d", h, m, s) : String(format: "%d:%02d", m, s)
    }

    /// "2,142 kg" in the display unit.
    static func mass(_ kg: Double, _ unit: WeightUnit) -> String {
        let v = unit == .lb ? kg / Maths.kgPerLb : kg
        return "\(v.rounded().formatted(.number.grouping(.automatic))) \(unit.rawValue)"
    }

    static func kgIn(_ kg: Double, _ unit: WeightUnit) -> String {
        Maths.tenths(unit == .lb ? kg / Maths.kgPerLb : kg)
    }

    /// "today", "yesterday", or "7 Oct".
    static func relativeDay(_ date: Date, now: Date = Date()) -> String {
        let cal = Calendar.current
        if cal.isDate(date, inSameDayAs: now) { return "today" }
        if cal.isDate(date, inSameDayAs: cal.date(byAdding: .day, value: -1, to: now)!) { return "yesterday" }
        return shortDay(date)
    }

    static func defaultWorkoutName(_ date: Date) -> String {
        date.formatted(.dateTime.weekday(.wide)) + " workout"
    }

    /// "Sat 7", "Tue 6 Oct", with the year when it isn't this one.
    static func day(_ date: Date, now: Date = Date()) -> String {
        let cal = Calendar.current
        if cal.isDate(date, equalTo: now, toGranularity: .year) {
            return date.formatted(.dateTime.weekday(.abbreviated).day().month(.abbreviated))
        }
        return date.formatted(.dateTime.day().month(.abbreviated).year())
    }

    /// "Tuesday 10 November".
    static func longDay(_ date: Date) -> String {
        "\(date.formatted(.dateTime.weekday(.wide))) \(date.formatted(.dateTime.day())) \(date.formatted(.dateTime.month(.wide)))"
    }

    static func shortDay(_ date: Date) -> String {
        date.formatted(.dateTime.day().month(.abbreviated))
    }

    /// "Chest, triceps, front shoulders" from the library's muscle names.
    static func muscles(_ list: [String]) -> String {
        let names = list.map { m in
            switch m {
            case "front delts": "front shoulders"
            case "side delts": "side shoulders"
            case "rear delts": "rear shoulders"
            default: m
            }
        }
        guard let first = names.first else { return "" }
        return ([first.prefix(1).uppercased() + first.dropFirst()] + names.dropFirst()).joined(separator: ", ")
    }
}
