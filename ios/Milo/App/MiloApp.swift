import SwiftUI

@main
struct MiloApp: App {
    @State private var app: AppModel

    init() {
        do {
            _app = State(initialValue: AppModel(db: try .onDisk()))
        } catch {
            fatalError("Milo can't open its database: \(error)")
        }
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(app)
                .preferredColorScheme(.dark)
                .tint(.or)
        }
    }
}

struct RootView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.scenePhase) private var phase

    var body: some View {
        Group {
            if app.session.isSignedIn {
                MainView()
            } else {
                SignInView()
            }
        }
        .animation(.smooth, value: app.session.isSignedIn)
        .task(id: app.session.isSignedIn) { await app.sync.sync() }
        .onChange(of: phase) { _, now in
            if now == .active {
                Task {
                    await app.health.importBody()
                    await app.sync.sync()
                }
            }
        }
    }
}
