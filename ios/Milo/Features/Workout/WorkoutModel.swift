import GRDB
import Foundation
import Observation
import OSLog
import UIKit

/// The running workout's screen state: which exercise is open, which number is
/// being typed, the rest timer, and the add field.
@Observable
final class WorkoutModel {
    let app: AppModel
    let detail = Live<WorkoutDetail?>(nil)

    /// The exercise shown open; nil means the first one not finished.
    var openID: UUID?
    var editing: Editing?
    /// What the pad has typed for the field being edited.
    var padText = ""
    /// The first key replaces the old value; later keys add to it.
    var padFresh = true
    var restEndsAt: Date?
    var restTotal: TimeInterval = 60
    var restForSet = 0
    var query = ""

    struct Editing: Equatable {
        enum Field { case weight, reps, seconds }
        var setID: UUID
        var field: Field
    }

    init(app: AppModel) {
        self.app = app
    }

    func watch() {
        let db = app.db
        detail.watch(db) { d in
            guard let w = try db.runningWorkout(d) else { return nil }
            return try db.workoutDetail(d, id: w.id)
        }
    }

    var entries: [ExerciseEntry] { detail.value?.entries ?? [] }

    /// The open exercise: the one tapped, else the first not finished, else the last.
    var current: ExerciseEntry? {
        if let openID, let e = entries.first(where: { $0.id == openID }) { return e }
        return entries.first { !$0.isFinished } ?? entries.last
    }

    var finished: [ExerciseEntry] { entries.filter { $0.isFinished && $0.id != current?.id } }
    var upNext: [ExerciseEntry] { entries.filter { !$0.isFinished && $0.id != current?.id } }

    // MARK: Sets

    func tick(_ set: LoggedSet, in entry: ExerciseEntry) {
        closePad()
        do {
            let done = try app.db.toggleDone(set.id)
            app.edited()
            UIImpactFeedbackGenerator(style: .medium).impactOccurred()
            if done {
                let rest = entry.restS ?? (try? app.db.writer.read { try app.db.settings($0) })?.restDefaultS ?? 60
                startRest(seconds: rest, nextSet: (entry.sets.firstIndex { $0.id == set.id } ?? 0) + 2)
                if entry.sets.allSatisfy({ $0.id == set.id || $0.done }) { openID = nil }
            }
        } catch {
            log.error("tick: \(error.localizedDescription)")
        }
    }

    func addSet(to entry: ExerciseEntry) {
        _ = try? app.db.addSet(to: entry.id)
        app.edited()
    }

    func delete(_ set: LoggedSet) {
        try? app.db.deleteSet(set.id)
        app.edited()
    }

    func remove(_ entry: ExerciseEntry) {
        try? app.db.removeExercise(entry.id)
        openID = nil
        app.edited()
    }

    func setUnit(_ unit: WeightUnit, for entry: ExerciseEntry) {
        try? app.db.setUnit(unit, for: entry.exercise.id)
        app.edited()
    }

    func setRest(_ seconds: Int, for entry: ExerciseEntry) {
        try? app.db.setRest(seconds, for: entry.exercise.id)
        app.edited()
    }

    func add(_ exercise: Exercise) {
        guard let w = detail.value?.workout else { return }
        if let we = try? app.db.addExercise(exercise.id, to: w.id) {
            openID = we.id
        }
        query = ""
        app.edited()
    }

    // MARK: The pad

    func edit(_ set: LoggedSet, _ field: Editing.Field, in entry: ExerciseEntry) {
        editing = Editing(setID: set.id, field: field)
        padFresh = true
        padText = switch field {
        case .weight: Format.weight(set, in: entry.unit)
        case .reps: set.reps.map(String.init) ?? ""
        case .seconds: set.durationS.map(String.init) ?? ""
        }
        UISelectionFeedbackGenerator().selectionChanged()
    }

    func closePad() {
        editing = nil
    }

    /// The set and exercise being edited.
    var editingTarget: (LoggedSet, ExerciseEntry)? {
        guard let editing else { return nil }
        for e in entries {
            if let s = e.sets.first(where: { $0.id == editing.setID }) { return (s, e) }
        }
        return nil
    }

    func key(_ k: String) {
        guard let editing else { return }
        if k == "⌫" {
            padText = String(padText.dropLast())
            padFresh = false
            save()
            return
        }
        var text = padFresh ? "" : padText
        padFresh = false
        if k == "." {
            guard editing.field == .weight, !text.contains(".") else { return }
            text = (text.isEmpty ? "0" : text) + "."
        } else {
            text += k
            let parts = text.split(separator: ".", omittingEmptySubsequences: false)
            let decimals = parts.count > 1 ? parts[1].count : 0
            guard text.count <= 7, decimals <= (editing.field == .weight ? 3 : 0) else { return }
        }
        padText = text
        save()
    }

    /// +2.5 kg or +5 lb (and the minus).
    func step(_ direction: Double) {
        guard let (_, entry) = editingTarget, editing?.field == .weight else { return }
        let step = entry.unit == .lb ? 5.0 : 2.5
        let value = max(0, (Double(padText) ?? 0) + direction * step)
        padText = Format.trim(String(format: "%.3f", value))
        padFresh = true
        save()
    }

    func toggleUnit() {
        guard let (_, entry) = editingTarget else { return }
        let to: WeightUnit = entry.unit == .kg ? .lb : .kg
        if editing?.field == .weight, let v = Maths.toKg(padText, entry.unit) {
            padText = Maths.tenths(to == .lb ? v / Maths.kgPerLb : v)
        }
        setUnit(to, for: entry)
        padFresh = true
    }

    /// Weight → reps → the next set's weight.
    func next() {
        guard let (set, entry) = editingTarget, let editing else { return }
        switch editing.field {
        case .weight:
            edit(set, entry.exercise.measurement == .timed ? .seconds : .reps, in: entry)
        case .reps, .seconds:
            if let i = entry.sets.firstIndex(where: { $0.id == set.id }), i + 1 < entry.sets.count {
                let following = entry.sets[i + 1]
                edit(following, entry.exercise.measurement == .repsLoad || entry.exercise.measurement == .assisted ? .weight : .reps, in: entry)
            } else {
                closePad()
            }
        }
    }

    private func save() {
        guard let (set, entry) = editingTarget, let editing else { return }
        var s = set
        switch editing.field {
        case .weight:
            s.weight = padText.hasSuffix(".") ? String(padText.dropLast()) : padText
            s.unit = entry.unit
        case .reps:
            s.reps = Int(padText)
        case .seconds:
            s.durationS = Int(padText)
        }
        try? app.db.update(s)
        app.edited()
    }

    // MARK: Rest

    func startRest(seconds: Int, nextSet: Int) {
        restTotal = TimeInterval(seconds)
        restEndsAt = Date().addingTimeInterval(restTotal)
        restForSet = nextSet
    }

    func addRest(_ seconds: TimeInterval) {
        guard let end = restEndsAt else { return }
        restEndsAt = end.addingTimeInterval(seconds)
        restTotal += seconds
    }

    func skipRest() {
        restEndsAt = nil
    }
}
