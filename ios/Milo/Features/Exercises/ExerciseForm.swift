import OSLog
import SwiftUI

/// Make an exercise of your own, or change one you made. Built-ins can't be changed.
struct ExerciseForm: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    /// The exercise being changed; nil makes a new one.
    var exercise: Exercise?
    var suggestedName = ""
    var onSave: (Exercise) -> Void = { _ in }

    @State private var name = ""
    @State private var equipment = "barbell"
    @State private var measurement = MeasurementKind.repsLoad
    @State private var unilateral = false
    @FocusState private var naming: Bool

    /// The built-in library's equipment, so your own sort and name the same way.
    static let equipment = ["barbell", "dumbbell", "machine", "cable", "smith machine", "kettlebell", "bodyweight", "plate", "other"]

    private var base: String { name.trimmingCharacters(in: .whitespacesAndNewlines) }

    /// "Landmine Rotation (Barbell)", like the built-ins.
    private var fullName: String {
        equipment == "other" ? base : "\(base) (\(equipment.capitalized))"
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Name, e.g. Landmine Rotation", text: $name)
                        .focused($naming)
                        .textInputAutocapitalization(.words)
                } footer: {
                    if !base.isEmpty { Text("Shows as \(fullName)") }
                }
                Section {
                    Picker("Equipment", selection: $equipment) {
                        ForEach(Self.equipment, id: \.self) { Text($0.capitalized).tag($0) }
                    }
                    Picker("Logged as", selection: $measurement) {
                        Text("Weight and reps").tag(MeasurementKind.repsLoad)
                        Text("Reps, bodyweight").tag(MeasurementKind.bodyweight)
                        Text("Reps, with assistance").tag(MeasurementKind.assisted)
                        Text("Time").tag(MeasurementKind.timed)
                        Text("Distance").tag(MeasurementKind.distance)
                    }
                    Toggle("One side at a time", isOn: $unilateral).tint(.or)
                } footer: {
                    Text("One side at a time logs each set per side, like 20 kg × 12 each side.")
                }
            }
            .font(.body16)
            .scrollContentBackground(.hidden)
            .miloScreen()
            .navigationTitle(exercise == nil ? "New exercise" : "Edit exercise")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save", action: save).disabled(base.isEmpty)
                }
            }
            .onAppear(perform: load)
        }
    }

    private func load() {
        if let e = exercise {
            name = e.shortName
            equipment = Self.equipment.contains(e.equipment) ? e.equipment : "other"
            measurement = e.measurement
            unilateral = e.unilateral
        } else {
            name = suggestedName
            naming = true
        }
    }

    private func save() {
        do {
            if var e = exercise {
                e.name = fullName
                e.equipment = equipment
                e.measurement = measurement
                e.unilateral = unilateral
                try app.db.save(e)
                onSave(e)
            } else {
                onSave(try app.db.createExercise(name: fullName, equipment: equipment, measurement: measurement, unilateral: unilateral))
            }
            app.edited()
            dismiss()
        } catch {
            log.error("save exercise: \(error.localizedDescription)")
        }
    }
}
