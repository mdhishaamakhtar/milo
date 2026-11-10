import SwiftUI

/// Finished workouts as rows: date, name, length, volume. Swipe or long-press to delete.
struct WorkoutList: View {
    @Environment(AppModel.self) private var app
    let workouts: [WorkoutSummary]
    @State private var deleting: WorkoutSummary?

    var body: some View {
        VStack(spacing: 0) {
            ForEach(Array(workouts.enumerated()), id: \.element.id) { i, w in
                if i > 0 { Divider().overlay(Color.line).padding(.leading, 72) }
                NavigationLink(value: WorkoutRoute(id: w.workout.id)) { row(w) }
                    .buttonStyle(.plain)
                    .background(Color.s1)
                    .swipeToDelete("Delete workout", cornerRadius: 0) { deleting = w }
                    .contextMenu {
                        Button("Do it again", systemImage: "play") { app.startWorkout(copying: w.workout.id) }
                        Button("Delete workout", systemImage: "trash", role: .destructive) { deleting = w }
                    }
            }
        }
        .background(Color.s1)
        .clipShape(.rect(cornerRadius: Radius.card))
        .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
        .deleteWorkoutDialog($deleting)
    }

    private func row(_ w: WorkoutSummary) -> some View {
        HStack(spacing: 12) {
            VStack(spacing: 0) {
                Text(w.workout.startedAt.formatted(.dateTime.weekday(.abbreviated))).font(.sofia(12, .semibold)).foregroundStyle(Color.tx3)
                Text(w.workout.startedAt.formatted(.dateTime.day())).font(.sofia(19, .bold))
            }
            .frame(width: 44)
            VStack(alignment: .leading, spacing: 2) {
                Text(w.workout.name).font(.h3).lineLimit(1)
                Text(detail(w)).font(.sub).foregroundStyle(Color.tx2)
            }
            Spacer(minLength: 8)
            Text(Format.mass(w.volumeKg, .kg)).font(.sofia(14.5, .regular)).foregroundStyle(Color.tx2)
            Image(systemName: "chevron.right").font(.system(size: 13, weight: .semibold)).foregroundStyle(Color.tx3)
        }
        .padding(.horizontal, 16)
        .frame(minHeight: 64)
        .contentShape(.rect)
    }

    private func detail(_ w: WorkoutSummary) -> String {
        let n = "\(w.exerciseCount) \(w.exerciseCount == 1 ? "exercise" : "exercises")"
        return w.minutes.map { "\($0) min, \(n)" } ?? n
    }
}

extension View {
    /// "Delete Legs?" for a workout, deleting it here and from Apple Health.
    func deleteWorkoutDialog(_ workout: Binding<WorkoutSummary?>, onDelete: @escaping () -> Void = {}) -> some View {
        modifier(DeleteWorkoutDialog(workout: workout, onDelete: onDelete))
    }
}

private struct DeleteWorkoutDialog: ViewModifier {
    @Environment(AppModel.self) private var app
    @Binding var workout: WorkoutSummary?
    let onDelete: () -> Void

    func body(content: Content) -> some View {
        content.confirmationDialog(
            "Delete \(workout?.workout.name ?? "workout")?",
            isPresented: Binding(get: { workout != nil }, set: { if !$0 { workout = nil } }),
            titleVisibility: .visible,
            presenting: workout
        ) { w in
            Button("Delete workout", role: .destructive) {
                app.deleteWorkout(w.workout)
                onDelete()
            }
        } message: { w in
            Text(w.workout.healthUUID == nil
                ? "Its \(w.doneSets) sets go too. This can't be undone."
                : "Its \(w.doneSets) sets go too, and it comes out of Apple Health. This can't be undone.")
        }
    }
}
