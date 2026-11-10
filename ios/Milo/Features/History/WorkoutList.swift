import SwiftUI

/// Finished workouts as rows: date, name, length, volume. Swipe or long-press to delete.
struct WorkoutList: View {
    @Environment(AppModel.self) private var app
    let workouts: [WorkoutSummary]

    var body: some View {
        VStack(spacing: 0) {
            ForEach(Array(workouts.enumerated()), id: \.element.id) { i, w in
                if i > 0 { Divider().overlay(Color.line).padding(.leading, 72) }
                NavigationLink(value: WorkoutRoute(id: w.workout.id)) { row(w) }
                    .buttonStyle(.row)
                    .background(Color.s1)
                    .swipeToDelete("Delete workout", cornerRadius: 0) { app.deleteWorkout(w.workout) }
                    .contextMenu {
                        Button("Do it again", systemImage: "play") { app.startWorkout(copying: w.workout.id) }
                        Button("Delete workout", systemImage: "trash", role: .destructive) { app.deleteWorkout(w.workout) }
                    }
            }
        }
        .background(Color.s1)
        .clipShape(.rect(cornerRadius: Radius.card))
        .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
    }

    private func row(_ w: WorkoutSummary) -> some View {
        HStack(spacing: 12) {
            VStack(spacing: 0) {
                Text(w.workout.startedAt.formatted(.dateTime.weekday(.abbreviated))).font(.sofia(12, .semibold)).foregroundStyle(Color.tx3)
                Text(w.workout.startedAt.formatted(.dateTime.day())).font(.sofia(19, .bold))
            }
            .frame(width: 44)
            VStack(alignment: .leading, spacing: Space.rowText) {
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
