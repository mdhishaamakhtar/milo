import GRDB
import Foundation
import Observation
import OSLog
import UIKit

/// The workout screen's state: which exercise is open, which number is being typed,
/// and the add field. It shows the running workout, or edits a finished one. The rest timer
/// is the app's (`AppModel.rest`), so it keeps going while the screen is tucked away.
@Observable
final class WorkoutModel {
    let app: AppModel
    /// nil follows the running workout; an id edits that finished one.
    let workoutID: UUID?
    let detail = Live<WorkoutDetail?>(nil)
    let settings = Live(UserSettings.fresh)

    /// The exercise shown open; nil means the first one not finished.
    var openID: UUID?
    var editing: Editing?
    /// What the pad has typed for the field being edited.
    var padText = ""
    /// The first key replaces the old value; later keys add to it.
    var padFresh = true
    var query = ""

    struct Editing: Equatable {
        enum Field { case weight, reps, seconds }
        var setID: UUID
        var field: Field
    }

    init(app: AppModel, workoutID: UUID? = nil) {
        self.app = app
        self.workoutID = workoutID
    }

    func watch() {
        let db = app.db, workoutID = workoutID
        detail.watch(db) { d in
            guard let id = try workoutID ?? db.runningWorkout(d)?.id else { return nil }
            return try db.workoutDetail(d, id: id)
        }
        settings.watch(db) { try db.settings($0) }
    }

    /// Editing a finished workout: new sets count as done and there's no rest timer.
    var isPast: Bool { detail.value?.workout.endedAt != nil }

    /// Sets in the workout that aren't ticked, which saving drops.
    var untickedCount: Int { entries.reduce(0) { $0 + $1.sets.count - $1.doneSets.count } }

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
            if done && !isPast {
                startRest(after: set, in: entry)
                if entry.sets.allSatisfy({ $0.id == set.id || $0.done }) { openID = nil }
            }
        } catch {
            log.error("tick: \(error.localizedDescription)")
        }
    }

    func addSet(to entry: ExerciseEntry) {
        app.write("add the set", { try app.db.addSet(to: entry.id, done: isPast) })
        app.edited()
    }

    func setKind(_ kind: SetKind, for set: LoggedSet) {
        var s = set
        s.kind = kind
        app.write("save the set", { try app.db.update(s) })
        app.edited()
    }

    func delete(_ set: LoggedSet) {
        if editing?.setID == set.id { closePad() }
        app.write("delete the set", { try app.db.deleteSet(set.id) })
        app.edited()
    }

    /// Takes an exercise out at once. If sets were logged, Undo brings it back with them.
    func remove(_ entry: ExerciseEntry) {
        if let editing, entry.sets.contains(where: { $0.id == editing.setID }) { closePad() }
        guard let rows = app.write("remove the exercise", { try app.db.removeExercise(entry.id) }) else { return }
        if openID == entry.id { openID = nil }
        app.edited()
        if !entry.doneSets.isEmpty { app.offerUndo("Removed \(entry.exercise.shortName)", rows: rows) }
    }

    /// Saves edits to a finished workout, and updates Health if it was saved there.
    func saveEdits() {
        guard let w = detail.value?.workout, let end = w.endedAt else { return }
        closePad()
        do {
            try app.db.finishWorkout(w.id, name: w.name, notes: w.notes, at: end)
            if w.healthUUID != nil { Task { [health = app.health] in await health.save(workoutID: w.id) } }
            app.edited()
        } catch {
            log.error("save edits: \(error.localizedDescription)")
        }
    }

    /// Throws the whole workout away. Undo brings it back, still running.
    func discard() {
        guard let w = detail.value?.workout else { return }
        closePad()
        app.rest.stop()
        app.deleteWorkout(w, message: "Workout discarded")
    }

    func setUnit(_ unit: WeightUnit, for entry: ExerciseEntry) {
        app.write("change the unit", { try app.db.setUnit(unit, for: entry.exercise.id) })
        app.edited()
    }

    func setRest(_ seconds: Int, for entry: ExerciseEntry) {
        app.write("change the rest timer", { try app.db.setRest(seconds, for: entry.exercise.id) })
        app.edited()
    }

    func add(_ exercise: Exercise) {
        guard let w = detail.value?.workout else { return }
        if let we = app.write("add the exercise", { try app.db.addExercise(exercise.id, to: w.id) }) {
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
        app.write("save the set", { try app.db.update(s) })
        app.edited()
    }

    // MARK: Rest

    /// Starts the rest after a set: until the next set not yet ticked, or the next exercise.
    private func startRest(after set: LoggedSet, in entry: ExerciseEntry) {
        let seconds = entry.restS ?? settings.value.restDefaultS
        if let i = entry.sets.firstIndex(where: { !$0.done && $0.id != set.id }) {
            app.rest.start(seconds: seconds, next: "set \(i + 1)", alert: "\(entry.exercise.shortName), set \(i + 1)")
        } else if let e = entries.first(where: { $0.id != entry.id && !$0.isFinished }) {
            app.rest.start(seconds: seconds, next: e.exercise.shortName, alert: "Next: \(e.exercise.shortName)")
        } else {
            app.rest.start(seconds: seconds, next: nil, alert: "")
        }
    }
}
