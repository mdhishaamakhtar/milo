import SwiftUI

/// Three tabs, the Ask Milo bar above them, and the workout, Milo and Profile on top.
struct MainView: View {
    @Environment(AppModel.self) private var app
    @State private var tab = TabID.today
    @Namespace private var workoutSpace

    enum TabID { case today, history, progress }

    var body: some View {
        @Bindable var app = app
        TabView(selection: $tab) {
            Tab("Today", systemImage: "dumbbell", value: .today) {
                NavigationStack { TodayView().miloDestinations() }
            }
            Tab("History", systemImage: "calendar", value: .history) {
                NavigationStack { HistoryView().miloDestinations() }
            }
            Tab("Progress", systemImage: "chart.line.uptrend.xyaxis", value: .progress) {
                NavigationStack { ProgressTab().miloDestinations() }
            }
        }
        .tabViewBottomAccessory {
            AskMiloBar(prompt: app.miloContext.prompt) { app.showingMilo = true }
        }
        .tabBarMinimizeBehavior(.onScrollDown)
        .environment(\.workoutSpace, workoutSpace)
        // The workout grows out of its card on Today and tucks back into it.
        .fullScreenCover(isPresented: $app.showingWorkout) {
            WorkoutView().navigationTransition(.zoom(sourceID: WorkoutSource.id, in: workoutSpace))
        }
        .sheet(isPresented: $app.showingMilo, onDismiss: app.miloClosed) { MiloView() }
        .sheet(isPresented: $app.showingProfile) { ProfileView() }
    }
}

/// The bar that opens Milo, with a prompt that follows the screen.
struct AskMiloBar: View {
    let prompt: String
    let open: () -> Void

    var body: some View {
        Button(action: open) {
            HStack(spacing: 11) {
                BarMLogo(size: 18)
                Text(prompt)
                    .font(.sofia(16.5, .regular))
                    .foregroundStyle(Color.tx2)
                    .lineLimit(1)
                Spacer(minLength: 0)
                Image(systemName: "mic")
                    .font(.system(size: 17, weight: .medium))
                    .foregroundStyle(Color.tx2)
                    .frame(width: 36, height: 36)
            }
            .padding(.leading, 17)
            .padding(.trailing, 8)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(prompt)
        .accessibilityHint("Opens Milo")
    }
}

/// The card on Today that the workout screen opens out of.
enum WorkoutSource {
    static let id = "workout"
}

extension EnvironmentValues {
    @Entry var workoutSpace: Namespace.ID?
}

extension View {
    /// Marks the view the workout screen zooms out of and back into.
    func workoutSource() -> some View { modifier(WorkoutSourceModifier()) }
}

private struct WorkoutSourceModifier: ViewModifier {
    @Environment(\.workoutSpace) private var space

    func body(content: Content) -> some View {
        if let space {
            content.matchedTransitionSource(id: WorkoutSource.id, in: space) { $0.clipShape(.rect(cornerRadius: Radius.card)) }
        } else {
            content
        }
    }
}

/// Where a tab's stack can go: a past workout or an exercise.
struct WorkoutRoute: Hashable { let id: UUID }
struct ExerciseRoute: Hashable { let id: UUID }

extension View {
    func miloDestinations() -> some View {
        navigationDestination(for: WorkoutRoute.self) { WorkoutDetailView(workoutID: $0.id) }
            .navigationDestination(for: ExerciseRoute.self) { ExerciseDetailView(exerciseID: $0.id) }
    }
}

/// Large title with a small line above it, and the avatar (or another button).
struct ScreenHeader<Trailing: View>: View {
    let eyebrow: String
    let title: String
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(alignment: .bottom) {
            VStack(alignment: .leading, spacing: 4) {
                Text(eyebrow).font(.sub).foregroundStyle(Color.tx2)
                Text(title).font(.large).tracking(-0.75)
            }
            Spacer()
            trailing.padding(.bottom, 2)
        }
        .padding(.horizontal, 4)
        .padding(.top, 8)
        .padding(.bottom, 18)
    }
}

extension ScreenHeader where Trailing == ProfileAvatar {
    init(eyebrow: String, title: String) {
        self.init(eyebrow: eyebrow, title: title) { ProfileAvatar() }
    }
}

/// The signed-in user's initial; opens Profile.
struct ProfileAvatar: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        AvatarButton(initial: String(app.session.user?.username.prefix(1).uppercased() ?? "M")) {
            app.showingProfile = true
        }
    }
}
