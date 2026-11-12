import Foundation
import Observation
import UserNotifications

/// The rest between sets. It belongs to the app, not the workout screen, so it keeps counting
/// while the workout is tucked away. When rest is over iOS says so with a local notification,
/// which reaches the lock screen and other apps where the bar can't. Local notifications need
/// no push service, so they work without a paid developer account.
@Observable
final class RestTimer: NSObject {
    private(set) var endsAt: Date?
    private(set) var total: TimeInterval = 60
    /// What comes after the rest: "set 2", or the next exercise. Nil after the last set.
    private(set) var next: String?
    /// The bar is on screen and says it itself, so the notification only makes its sound.
    @ObservationIgnored var barShowing = false
    /// Tapping the notification opens the workout.
    @ObservationIgnored var openWorkout: @MainActor () -> Void = {}
    @ObservationIgnored private var alert = ""
    @ObservationIgnored private var clearing: Task<Void, Never>?
    private static let id = "rest"

    /// `alert` is the notification's line under "Rest over", e.g. "Squat, set 2".
    func start(seconds: Int, next: String?, alert: String) {
        total = TimeInterval(seconds)
        self.next = next
        self.alert = alert
        endsAt = .now.addingTimeInterval(total)
        scheduled()
    }

    func add(_ seconds: TimeInterval) {
        guard let end = endsAt else { return }
        endsAt = end.addingTimeInterval(seconds)
        total += seconds
        scheduled()
    }

    func stop() {
        endsAt = nil
        clearing?.cancel()
        UNUserNotificationCenter.current().removePendingNotificationRequests(withIdentifiers: [Self.id])
    }

    /// Sets the notification for the new end, replacing any earlier one, and takes the bar
    /// away a few seconds after it.
    private func scheduled() {
        guard let end = endsAt else { return }
        clearing?.cancel()
        clearing = Task {
            try? await Task.sleep(for: .seconds(end.timeIntervalSinceNow + 3))
            if !Task.isCancelled, endsAt == end { endsAt = nil }
        }

        let content = UNMutableNotificationContent()
        content.title = "Rest over"
        content.body = alert
        content.sound = .default
        let trigger = UNTimeIntervalNotificationTrigger(timeInterval: max(1, end.timeIntervalSinceNow), repeats: false)
        let request = UNNotificationRequest(identifier: Self.id, content: content, trigger: trigger)
        let center = UNUserNotificationCenter.current()
        Task {
            // Asked the first time a rest starts, when it's plain what it's for.
            guard (try? await center.requestAuthorization(options: [.alert, .sound])) == true, endsAt == end else { return }
            try? await center.add(request)
        }
    }
}

extension RestTimer: UNUserNotificationCenterDelegate {
    /// With Milo open, the alert shows as a banner, except over the bar that already says it.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        await barShowing ? [.sound] : [.banner, .sound]
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        await openWorkout()
    }
}
