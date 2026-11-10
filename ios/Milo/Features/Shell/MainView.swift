import SwiftUI

/// Three tabs, the Ask Milo bar above them, and the workout, Milo and Profile on top.
struct MainView: View {
    @Environment(AppModel.self) private var app
    @Namespace private var workoutSpace

    var body: some View {
        @Bindable var app = app
        TabView(selection: $app.tab) {
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
        .undoBar(above: 132)
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
    /// Above the tab bar it shows the full prompt; beside a minimised tab bar, just "Ask Milo".
    @Environment(\.tabViewBottomAccessoryPlacement) private var placement

    var body: some View {
        Button(action: open) {
            HStack(spacing: 11) {
                HStack(alignment: .firstTextBaseline, spacing: 11) {
                    BarMLogo(size: 18)
                    Text(placement == .inline ? "Ask Milo" : prompt)
                        .font(.sofia(16.5, .regular))
                        .foregroundStyle(Color.tx2)
                        .lineLimit(1)
                }
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

/// Where a stack can go: a past workout, an exercise, or a list reached from more than one place.
/// Every push in these stacks is by value. A view pushed with `NavigationLink { View() }` sits
/// after the path, so a value pushed from it lands underneath it and Back runs in the wrong order.
struct WorkoutRoute: Hashable { let id: UUID }
struct ExerciseRoute: Hashable { let id: UUID }
enum ScreenRoute: Hashable { case exercises, body }

extension View {
    func miloDestinations() -> some View {
        navigationDestination(for: WorkoutRoute.self) { WorkoutDetailView(workoutID: $0.id) }
            .navigationDestination(for: ExerciseRoute.self) { ExerciseDetailView(exerciseID: $0.id) }
            .navigationDestination(for: ScreenRoute.self) { route in
                switch route {
                case .exercises: ExercisesView()
                case .body: BodyView()
                }
            }
    }
}

/// The top of each tab: Milo's name at the leading edge, on the bar itself rather than glass
/// (it isn't a control), and the signed-in user's initial on glass at the trailing edge,
/// which opens Profile.
struct TabToolbar: ToolbarContent {
    @Environment(AppModel.self) private var app

    var body: some ToolbarContent {
        ToolbarItem(placement: .topBarLeading) {
            MiloWordmark().fixedSize()
        }
        .sharedBackgroundVisibility(.hidden)
        ToolbarItem(placement: .topBarTrailing) {
            Button { app.showingProfile = true } label: {
                Text(app.session.user?.username.prefix(1).uppercased() ?? "M")
                    .font(.sofia(17, .bold))
            }
            .tint(Color.tx)
            .accessibilityLabel("Profile")
        }
    }
}

extension View {
    /// A top-level tab's standard navigation bar: Milo's name and Profile, then a large title
    /// that shrinks as you scroll, with a line under it.
    func tabTitle(_ title: String, subtitle: String) -> some View {
        navigationTitle(title)
            .navigationSubtitle(subtitle)
            .toolbar { TabToolbar() }
    }
}
