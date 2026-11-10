import Connect
import MiloAPI
import SwiftUI

/// The current password, then a new one twice.
struct ChangePasswordView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var current = ""
    @State private var new = ""
    @State private var again = ""
    @State private var working = false
    @State private var problem: String?
    @State private var changed = false

    private var mismatch: Bool { !again.isEmpty && new != again }
    private var ready: Bool { !current.isEmpty && new.count >= 8 && new == again && !working }

    var body: some View {
        MiloForm {
            Section {
                SecureField("Current password", text: $current).textContentType(.password)
            }
            Section {
                SecureField("New password", text: $new).textContentType(.newPassword)
                SecureField("New password again", text: $again).textContentType(.newPassword)
            } footer: {
                Text(mismatch ? "The two new passwords don't match." : "At least 8 characters. You stay signed in here.")
                    .foregroundStyle(mismatch ? Color.red : Color.tx3)
            }
            if let problem {
                Section { Text(problem).foregroundStyle(Color.red) }
            }
            Section {
                Button(working ? "Changing" : "Change password", action: change)
                    .disabled(!ready)
            }
        }
        .navigationTitle("Change password")
        .navigationBarTitleDisplayMode(.inline)
        .alert("Password changed", isPresented: $changed) {
            Button("OK") { dismiss() }
        }
    }

    private func change() {
        working = true
        problem = nil
        Task {
            defer { working = false }
            var req = Milo_ChangePasswordRequest()
            req.currentPassword = current
            req.newPassword = new
            do {
                _ = try await app.api.auth.changePassword(request: req, headers: APIClient.headers(app.session.token)).value()
                changed = true
            } catch {
                problem = error.localizedDescription
            }
        }
    }
}

/// Deletes the account and everything in it, after the password and a last check.
struct DeleteAccountView: View {
    @Environment(AppModel.self) private var app
    @State private var password = ""
    @State private var confirming = false
    @State private var working = false
    @State private var problem: String?

    var body: some View {
        MiloForm {
            Section {
                Text("This deletes your account and every workout, template, exercise and conversation with Milo, on the server and on this phone. It can't be undone.")
                    .font(.sofia(15))
                NavigationLink("Export my data first") { ExportView() }
                    .foregroundStyle(Color.or)
            }
            Section {
                SecureField("Password", text: $password).textContentType(.password)
            } footer: {
                if let problem { Text(problem).foregroundStyle(Color.red) }
            }
            Section {
                Button(working ? "Deleting" : "Delete my account", role: .destructive) { confirming = true }
                    .foregroundStyle(Color.red)
                    .disabled(password.isEmpty || working)
                    .confirmationDialog("Delete your account for good?", isPresented: $confirming, titleVisibility: .visible) {
                        Button("Delete account and all data", role: .destructive, action: delete)
                    } message: {
                        Text("Workouts saved to Apple Health stay in Health.")
                    }
            }
        }
        .navigationTitle("Delete account")
        .navigationBarTitleDisplayMode(.inline)
    }

    private func delete() {
        working = true
        problem = nil
        Task {
            defer { working = false }
            var req = Milo_DeleteAccountRequest()
            req.password = password
            do {
                _ = try await app.api.auth.deleteAccount(request: req, headers: APIClient.headers(app.session.token)).value()
                app.showingProfile = false
                app.milo.reset()
                try app.session.forget()
            } catch {
                problem = error.localizedDescription
            }
        }
    }
}

/// Everything you've logged, as files to keep or open elsewhere.
struct ExportView: View {
    @Environment(AppModel.self) private var app
    @State private var files: Exporter.Files?
    @State private var problem: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                Text("Two files: one with everything (workouts, sets, templates, your exercises, body readings), and one with a row per set for a spreadsheet. They're made on this phone from what's on it.")
                    .font(.sofia(15)).foregroundStyle(Color.tx2)
                    .padding(.horizontal, 4)

                if let files {
                    VStack(spacing: 0) {
                        fileRow(files.json, "Everything", "JSON")
                        Divider().overlay(Color.line).padding(.leading, 60)
                        fileRow(files.csv, "Sets", "CSV, one row per set")
                    }
                    .background(Color.s1, in: .rect(cornerRadius: Radius.card))
                    .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
                    ShareLink(items: [files.json, files.csv]) { Text("Share both") }
                        .buttonStyle(.primary)
                } else {
                    Button("Make the export", action: make).buttonStyle(.primary)
                }
                if let problem { Text(problem).font(.sub).foregroundStyle(Color.red) }
            }
            .padding(.horizontal, 16)
            .padding(.top, 8)
        }
        .miloScreen()
        .navigationTitle("Export my data")
        .navigationBarTitleDisplayMode(.inline)
    }

    private func fileRow(_ url: URL, _ title: String, _ detail: String) -> some View {
        ShareLink(item: url) {
            HStack(spacing: 14) {
                Image(systemName: "doc").font(.system(size: 15)).foregroundStyle(Color.tx2)
                    .frame(width: 32, height: 32).background(Color.s2, in: .rect(cornerRadius: 9))
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).font(.sofia(16, .semibold))
                    Text("\(url.lastPathComponent), \(detail)").font(.sofia(13.5)).foregroundStyle(Color.tx2).lineLimit(1)
                }
                Spacer()
                Image(systemName: "square.and.arrow.up").foregroundStyle(Color.or)
            }
            .padding(.horizontal, 14)
            .frame(minHeight: 62)
            .contentShape(.rect)
        }
        .buttonStyle(.row)
    }

    private func make() {
        do {
            files = try Exporter.write(app.db)
        } catch {
            problem = "Couldn't make the export: \(error.localizedDescription)"
        }
    }
}
