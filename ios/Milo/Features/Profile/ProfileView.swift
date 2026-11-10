import GRDB
import MiloAPI
import SwiftUI

/// You: body and Health, how totals show, rest, your exercises, password, export, sign out.
struct ProfileView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var data = Live(ProfileData())
    @State private var signingOut = false

    struct ProfileData: Sendable {
        var settings = UserSettings.fresh
        var workouts = 0
        var latestKg: Double?
        var exercises = 0
    }

    var body: some View {
        NavigationStack {
            List {
                Section { header }
                    .listRowBackground(Color.clear)
                    .listRowInsets(EdgeInsets(top: 0, leading: 4, bottom: 4, trailing: 4))

                Section {
                    NavigationLink { BodyView() } label: {
                        SettingsRow(icon: "person", title: "Body", value: data.value.latestKg.map { "\(Format.kgIn($0, unit)) \(unit.rawValue)" } ?? "Add")
                    }
                    NavigationLink { HealthView() } label: {
                        SettingsRow(icon: "heart", title: "Apple Health", value: app.health.connected ? "On" : "Off", tint: .or)
                    }
                }
                .listRowBackground(Color.s1)
                Section {
                    Menu {
                        Picker("Totals and charts in", selection: Binding(get: { data.value.settings.displayUnit }, set: { u in update { $0.displayUnit = u } })) {
                            Text("Kilograms").tag(WeightUnit.kg)
                            Text("Pounds").tag(WeightUnit.lb)
                        }
                    } label: {
                        MenuRow(row: SettingsRow(icon: "ruler", title: "Totals and charts in", value: unit.rawValue))
                    }
                    Menu {
                        Picker("Rest timer", selection: Binding(get: { data.value.settings.restDefaultS }, set: { r in update { $0.restDefaultS = r } })) {
                            ForEach([30, 45, 60, 90, 120, 150, 180], id: \.self) { Text(Format.clock($0)).tag($0) }
                        }
                    } label: {
                        MenuRow(row: SettingsRow(icon: "timer", title: "Rest timer", value: Format.clock(data.value.settings.restDefaultS)))
                    }
                    NavigationLink { ExercisesView() } label: {
                        SettingsRow(icon: "line.3.horizontal", title: "Exercises", value: "\(data.value.exercises)")
                    }
                } footer: {
                    Text("Each exercise keeps its own unit and rest; these are the defaults.")
                        .font(.sofia(13)).foregroundStyle(Color.tx3)
                }
                .listRowBackground(Color.s1)
                Section {
                    NavigationLink { ChangePasswordView() } label: { SettingsRow(icon: "lock", title: "Change password") }
                    NavigationLink { ExportView() } label: { SettingsRow(icon: "square.and.arrow.up", title: "Export my data") }
                }
                .listRowBackground(Color.s1)
                Section {
                    Button("Sign out", role: .destructive) { signingOut = true }
                        .font(.sofia(16, .semibold))
                        .foregroundStyle(Color.red)
                        .frame(maxWidth: .infinity)
                }
                .listRowBackground(Color.s1)
                Section {
                    NavigationLink { DeleteAccountView() } label: {
                        Text("Delete account").font(.sofia(15, .medium)).foregroundStyle(Color.red)
                    }
                } footer: {
                    Text("Milo 0.1, alpha").font(.sofia(13)).foregroundStyle(Color.tx3).frame(maxWidth: .infinity).padding(.top, 8)
                }
                .listRowBackground(Color.s1)
            }
            .environment(\.defaultMinListRowHeight, 52)
            .listStyle(.insetGrouped)
            .listSectionSpacing(14)
            .contentMargins(.top, 0, for: .scrollContent)
            .scrollContentBackground(.hidden)
            .miloScreen()
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() }.tint(Color.tx) }
            }
            .confirmationDialog("Sign out of Milo?", isPresented: $signingOut, titleVisibility: .visible) {
                Button("Sign out", role: .destructive, action: signOut)
            } message: {
                Text("Your workouts stay saved. Anything not synced yet goes up first.")
            }
        }
        .tint(.or)
        .onAppear {
            let db = app.db
            data.watch(db) { d in
                ProfileData(
                    settings: try db.settings(d),
                    workouts: try Workout.filter(Column("deleted") == false && Column("endedAt") != nil).fetchCount(d),
                    latestKg: try db.latestBodyKg(d),
                    exercises: try db.myExerciseCount(d))
            }
        }
    }

    private var unit: WeightUnit { data.value.settings.displayUnit }

    private var header: some View {
        let name = app.session.user?.username ?? ""
        return HStack(spacing: 16) {
            Text(name.prefix(1).uppercased())
                .font(.sofia(26, .bold))
                .frame(width: 66, height: 66)
                .background(Color.s2, in: .circle)
                .overlay(Circle().strokeBorder(Color.line2))
            VStack(alignment: .leading, spacing: 3) {
                Text(name.prefix(1).uppercased() + name.dropFirst()).font(.sofia(24, .bold))
                Text("@\(name), \(data.value.workouts) \(data.value.workouts == 1 ? "workout" : "workouts")")
                    .font(.sub).foregroundStyle(Color.tx2)
            }
        }
    }

    /// Saves one change to the settings row.
    private func update(_ change: (inout UserSettings) -> Void) {
        var s = data.value.settings
        change(&s)
        try? app.db.save(s)
        app.edited()
    }

    private func signOut() {
        Task {
            await app.sync.sync()
            _ = await app.api.auth.logout(request: Milo_LogoutRequest(), headers: APIClient.headers(app.session.token))
            app.milo.reset()
            app.session.signOut()
            dismiss()
        }
    }
}

/// An icon in a small square, a title, and an optional value on the right.
struct SettingsRow: View {
    let icon: String
    let title: String
    var value: String?
    var tint: Color = .tx2

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: icon)
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(tint)
                .frame(width: 30, height: 30)
                .background(Color.s2, in: .rect(cornerRadius: 9))
            Text(title).font(.sofia(16, .semibold)).foregroundStyle(Color.tx)
            Spacer(minLength: 8)
            if let value { Text(value).font(.sofia(15)).foregroundStyle(Color.tx2) }
        }
    }
}

/// A settings row that opens a menu in place.
private struct MenuRow: View {
    let row: SettingsRow

    var body: some View {
        HStack(spacing: 8) {
            row
            Image(systemName: "chevron.up.chevron.down")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(Color.tx3)
        }
        .contentShape(.rect)
    }
}
