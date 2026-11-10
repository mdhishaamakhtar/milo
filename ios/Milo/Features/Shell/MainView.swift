import SwiftUI

/// Three tabs, the Ask Milo bar above them, and the workout, Milo and Profile on top.
struct MainView: View {
    @Environment(AppModel.self) private var app
    @State private var tab = TabID.today

    enum TabID { case today, history, progress }

    var body: some View {
        @Bindable var app = app
        TabView(selection: $tab) {
            Tab("Today", systemImage: "dumbbell", value: .today) {
                NavigationStack { TodayView() }
            }
            Tab("History", systemImage: "calendar", value: .history) {
                NavigationStack { HistoryView() }
            }
            Tab("Progress", systemImage: "chart.line.uptrend.xyaxis", value: .progress) {
                NavigationStack { ProgressTab() }
            }
        }
        .tabViewBottomAccessory {
            AskMiloBar(prompt: app.miloContext.prompt) { app.showingMilo = true }
        }
        .tabBarMinimizeBehavior(.onScrollDown)
        .fullScreenCover(isPresented: $app.showingWorkout) { WorkoutView() }
        .sheet(isPresented: $app.showingMilo) { MiloView() }
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

/// Large title with a small line above it, and the avatar.
struct ScreenHeader: View {
    @Environment(AppModel.self) private var app
    let eyebrow: String
    let title: String

    var body: some View {
        HStack(alignment: .bottom) {
            VStack(alignment: .leading, spacing: 4) {
                Text(eyebrow).font(.sub).foregroundStyle(Color.tx2)
                Text(title).font(.large).tracking(-0.75)
            }
            Spacer()
            AvatarButton(initial: String(app.session.user?.username.prefix(1).uppercased() ?? "M")) {
                app.showingProfile = true
            }
            .padding(.bottom, 2)
        }
        .padding(.horizontal, 4)
        .padding(.top, 8)
        .padding(.bottom, 18)
    }
}
