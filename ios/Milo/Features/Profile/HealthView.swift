import SwiftUI

/// What Milo writes to Apple Health and what it reads, each switchable.
struct HealthView: View {
    @Environment(AppModel.self) private var app
    @State private var connecting = false

    var body: some View {
        @Bindable var health = app.health
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                status

                SectionHeader("Milo writes")
                toggles([
                    ("Workouts", "As Traditional Strength Training", $health.writeWorkouts),
                    ("Active energy", "An estimate from your sets and weight", $health.writeEnergy),
                ])

                SectionHeader("Milo reads")
                toggles([
                    ("Body weight", "For calories and bodyweight sets", $health.readWeight),
                    ("Height, date of birth, sex", "For better estimates", $health.readProfile),
                    ("Body fat", "Optional", $health.readBodyFat),
                ])

                HStack(alignment: .top, spacing: 12) {
                    Image(systemName: "applewatch").font(.system(size: 15, weight: .medium)).foregroundStyle(Color.or)
                    VStack(alignment: .leading, spacing: 3) {
                        Text("Wearing an Apple Watch?").font(.sofia(15.5, .semibold))
                        Text("If you also start a workout on the watch, Health counts the session twice. Record it in one place.")
                            .font(.sofia(14)).foregroundStyle(Color.tx2)
                    }
                }
                .padding(16)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.s1, in: .rect(cornerRadius: 20))
                .overlay(RoundedRectangle(cornerRadius: 20).strokeBorder(Color.or.opacity(0.35)))
                .padding(.top, 16)

                if health.connected {
                    Button("Stop using Apple Health") { health.disconnect() }
                        .font(.link)
                        .foregroundStyle(Color.red)
                        .frame(maxWidth: .infinity, minHeight: 44)
                        .padding(.top, 16)
                    Text("Milo stops writing and reading. What's already in Health stays there; you can also turn access off in the Health app.")
                        .font(.sofia(13.5)).foregroundStyle(Color.tx3).multilineTextAlignment(.center)
                        .frame(maxWidth: .infinity)
                }
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 24)
        }
        .miloScreen()
        .navigationTitle("Apple Health")
        .navigationBarTitleDisplayMode(.inline)
        .onChange(of: health.readWeight) { _, on in if on { Task { await health.importBody() } } }
        .onChange(of: health.readProfile) { _, on in if on { Task { await health.importBody() } } }
        .onChange(of: health.readBodyFat) { _, on in if on { Task { await health.importBody() } } }
    }

    private var status: some View {
        HStack(spacing: 14) {
            Image(systemName: "heart").font(.system(size: 17, weight: .medium)).foregroundStyle(Color.or)
                .frame(width: 44, height: 44).background(Color.or.opacity(0.14), in: .rect(cornerRadius: 12))
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.sofia(17, .semibold))
                Text(subtitle).font(.sofia(14)).foregroundStyle(Color.tx2)
            }
            Spacer(minLength: 8)
            if app.health.available && !app.health.connected {
                Button {
                    connecting = true
                    Task {
                        await app.health.connect()
                        connecting = false
                    }
                } label: {
                    Text(connecting ? "Connecting" : "Connect").padding(.horizontal, 16)
                }
                .buttonStyle(PrimaryButtonStyle(compact: true))
                .fixedSize()
                .disabled(connecting)
            }
        }
        .card(padding: 16)
        .padding(.top, 8)
    }

    private var title: String {
        if !app.health.available { return "Not available" }
        return app.health.connected ? "Connected" : "Not connected"
    }

    private var subtitle: String {
        if !app.health.available { return "Apple Health isn't on this device." }
        if !app.health.connected { return "Save workouts and calories, read your weight." }
        return app.health.lastSaved.map { "Last saved \($0)" } ?? "Nothing saved yet"
    }

    private func toggles(_ rows: [(String, String, Binding<Bool>)]) -> some View {
        VStack(spacing: 0) {
            ForEach(Array(rows.enumerated()), id: \.offset) { i, row in
                if i > 0 { Divider().overlay(Color.line).padding(.leading, 16) }
                Toggle(isOn: row.2) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(row.0).font(.sofia(16, .semibold))
                        Text(row.1).font(.sofia(14)).foregroundStyle(Color.tx2)
                    }
                }
                .tint(.or)
                .padding(.horizontal, 16)
                .frame(minHeight: 62)
            }
        }
        .background(Color.s1, in: .rect(cornerRadius: Radius.card))
        .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
    }
}
