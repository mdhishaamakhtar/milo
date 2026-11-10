import GRDB
import OSLog
import SwiftUI

/// Ending a workout: the numbers, what changed since last time, Health, template, a note.
struct FinishView: View {
    @Environment(AppModel.self) private var app
    @Environment(WorkoutModel.self) private var model
    @State private var name = ""
    @State private var note = ""
    @State private var saveToHealth = true
    @State private var saveTemplate = false
    @State private var bodyKg: Double?
    @FocusState private var naming: Bool

    var body: some View {
        if let detail = model.detail.value {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    VStack(alignment: .leading, spacing: 4) {
                        HStack(spacing: 8) {
                            TextField("Workout name", text: $name).font(.title).focused($naming).fixedSize()
                            Image(systemName: "pencil").foregroundStyle(Color.tx3).onTapGesture { naming = true }
                        }
                        Text(timeRange(detail.workout.startedAt)).font(.sub).foregroundStyle(Color.tx2)
                    }
                    stats(detail)
                    exercises(detail)
                    VStack(spacing: 0) {
                        toggleRow("heart", "Save to Apple Health", detail: kcalText(detail), isOn: $saveToHealth)
                        Divider().overlay(Color.line).padding(.leading, 60)
                        toggleRow("list.bullet", "Save as a template", detail: nil, isOn: $saveTemplate)
                    }
                    .background(Color.s1, in: .rect(cornerRadius: Radius.card))
                    .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
                    HStack(spacing: 12) {
                        Image(systemName: "line.3.horizontal").foregroundStyle(Color.tx3)
                        TextField("Add a note about today", text: $note, axis: .vertical).font(.body16)
                    }
                    .padding(16)
                    .background(Color.s1, in: .rect(cornerRadius: 20))
                    .overlay(RoundedRectangle(cornerRadius: 20).strokeBorder(Color.line))
                }
                .padding(.horizontal, 16)
                .padding(.top, 8)
            }
            .safeAreaInset(edge: .bottom) {
                Button("Save workout") { save(detail) }
                    .buttonStyle(.primary)
                    .padding(.horizontal, 16)
                    .padding(.bottom, 8)
            }
            .miloScreen()
            .navigationTitle("Finish")
            .onAppear {
                name = detail.workout.name
                note = detail.workout.notes
                bodyKg = try? app.db.writer.read { try app.db.latestBodyKg($0) }
            }
        }
    }

    private func timeRange(_ start: Date) -> String {
        Format.longDay(start) + ", " + start.formatted(date: .omitted, time: .shortened) + " to " + Date.now.formatted(date: .omitted, time: .shortened)
    }

    private func stats(_ d: WorkoutDetail) -> some View {
        let minutes = max(1, Int(Date.now.timeIntervalSince(d.workout.startedAt) / 60))
        return HStack(spacing: 0) {
            stat("\(minutes)", "min", "Duration")
            Divider().overlay(Color.line).padding(.vertical, 4)
            stat(Format.mass(d.volumeKg, .kg).replacingOccurrences(of: " kg", with: ""), "kg", "Volume").padding(.leading, 16)
            Divider().overlay(Color.line).padding(.vertical, 4)
            stat("\(d.doneSetCount)", nil, "Sets").padding(.leading, 16)
        }
        .fixedSize(horizontal: false, vertical: true)
        .card()
    }

    private func stat(_ value: String, _ unit: String?, _ label: String) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(alignment: .firstTextBaseline, spacing: 3) {
                Text(value).font(.sofia(23, .bold))
                if let unit { Text(unit).font(.sofia(14, .semibold)).foregroundStyle(Color.tx2) }
            }
            Text(label).font(.cap).foregroundStyle(Color.tx3)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func exercises(_ d: WorkoutDetail) -> some View {
        VStack(spacing: 0) {
            ForEach(Array(d.entries.filter { !$0.doneSets.isEmpty }.enumerated()), id: \.element.id) { i, e in
                if i > 0 { Divider().overlay(Color.line).padding(.leading, 16) }
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(e.exercise.name).font(.sofia(16, .semibold)).lineLimit(1)
                        Text(Format.summary(e)).font(.sofia(14, .regular)).foregroundStyle(Color.tx2)
                    }
                    Spacer(minLength: 8)
                    if e.exercise.approximate {
                        Badge(text: "Approximate")
                    } else if let last = e.lastDate, Maths.volumeKg(e.inputs) > Maths.volumeKg(e.lastTime.map { $0.input(e.exercise.measurement) }) {
                        Badge(text: "↑ More than \(Format.shortDay(last))", color: .bl)
                    }
                }
                .padding(.horizontal, 16).frame(minHeight: 58)
            }
        }
        .background(Color.s1, in: .rect(cornerRadius: Radius.card))
        .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
    }

    private func toggleRow(_ icon: String, _ title: String, detail: String?, isOn: Binding<Bool>) -> some View {
        Toggle(isOn: isOn) {
            HStack(spacing: 14) {
                Image(systemName: icon).font(.system(size: 14)).foregroundStyle(icon == "heart" ? Color.or : Color.tx2)
                    .frame(width: 32, height: 32).background(Color.s2, in: .rect(cornerRadius: 10))
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).font(.sofia(16, .semibold))
                    if let detail { Text(detail).font(.sofia(14, .regular)).foregroundStyle(Color.tx2) }
                }
            }
        }
        .tint(.or)
        .padding(.horizontal, 14).frame(minHeight: 62)
    }

    private func kcalText(_ d: WorkoutDetail) -> String {
        let shares = d.entries.map { Maths.ExerciseShare(met: $0.exercise.met, doneSets: $0.doneSets.count) }
        guard let kcal = Maths.activeKcal(durationS: Date.now.timeIntervalSince(d.workout.startedAt), bodyKg: bodyKg ?? 0, exercises: shares) else {
            return "Strength training. Add your weight in Profile for calories."
        }
        return "Strength training, about \(Int(kcal.rounded())) kcal"
    }

    private func save(_ d: WorkoutDetail) {
        do {
            try app.db.finishWorkout(d.workout.id, name: name, notes: note)
            if saveTemplate { try app.db.templateFrom(workout: d.workout.id, name: name) }
            if saveToHealth { Task { await app.health.save(workoutID: d.workout.id) } }
            app.edited()
        } catch {
            log.error("finish: \(error.localizedDescription)")
        }
    }
}
