import SwiftUI

@main
struct MiloApp: App {
    @State private var app: AppModel

    init() {
        do {
            _app = State(initialValue: Self.hostingTests
                ? AppModel(db: try .inMemory(), keychain: Keychain(service: "dev.hishaam.milo.tests"), onDevice: false)
                : AppModel(db: try .onDisk()))
        } catch {
            fatalError("Milo can't open its database: \(error)")
        }
        Self.styleNavigationTitles()
    }

    /// Navigation titles in Sofia Sans, like the rest of the app. Only the text changes: the
    /// bar keeps the system's own (transparent, glass-buttoned) look.
    private static func styleNavigationTitles() {
        let text = UIColor(Color.tx)
        let appearance = UINavigationBarAppearance()
        appearance.configureWithTransparentBackground()
        appearance.largeTitleTextAttributes = [.font: UIFont.sofia(34, .bold), .foregroundColor: text]
        appearance.titleTextAttributes = [.font: UIFont.sofia(17, .semibold), .foregroundColor: text]
        appearance.largeSubtitleTextAttributes = [.font: UIFont.sofia(15, .regular), .foregroundColor: UIColor(Color.tx2)]
        appearance.subtitleTextAttributes = [.font: UIFont.sofia(13, .medium), .foregroundColor: UIColor(Color.tx2)]
        let bar = UINavigationBar.appearance()
        bar.standardAppearance = appearance
        bar.scrollEdgeAppearance = appearance
        bar.compactAppearance = appearance
    }

    /// The app is only hosting its tests: it shows nothing, so it doesn't open the real
    /// store, read the real Keychain or sync while they run.
    static let hostingTests = ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] != nil

    var body: some Scene {
        WindowGroup {
            if Self.hostingTests {
                Color.bg.ignoresSafeArea()
            } else {
                RootView()
                    .environment(app)
                    .preferredColorScheme(.dark)
                    .tint(.or)
            }
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
