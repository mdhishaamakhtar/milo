import MiloAPI
import SwiftUI

struct SignInView: View {
    @Environment(AppModel.self) private var app
    @State private var creating = false
    @State private var username = ""
    @State private var password = ""
    @State private var invite = ""
    @State private var busy = false
    @State private var problem: String?
    @FocusState private var focus: Field?

    private enum Field { case username, password, invite }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                RoundedRectangle(cornerRadius: 24)
                    .fill(Color.s1)
                    .overlay(RoundedRectangle(cornerRadius: 24).strokeBorder(Color.line2))
                    .overlay(BarMLogo(size: 42))
                    .frame(width: 76, height: 76)
                    .shadow(color: .black.opacity(0.45), radius: 20, y: 18)
                    .padding(.top, 90)
                Text("Milo")
                    .font(.sofia(44, .heavy, relativeTo: .largeTitle))
                    .tracking(-1.3)
                    .padding(.top, 26)
                Text("The workout log you can talk to.")
                    .font(.sofia(19, .regular, relativeTo: .title3))
                    .foregroundStyle(Color.tx2)
                    .padding(.top, 10)

                VStack(spacing: 0) {
                    field("Username", text: $username, field: .username)
                        .textContentType(.username)
                    Divider().overlay(Color.line).padding(.leading, 16)
                    field("Password", text: $password, field: .password, secure: true)
                        .textContentType(creating ? .newPassword : .password)
                    if creating {
                        Divider().overlay(Color.line).padding(.leading, 16)
                        field("Invite", text: $invite, field: .invite)
                    }
                }
                .background(Color.s1, in: .rect(cornerRadius: Radius.card))
                .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
                .padding(.top, 64)

                if let problem {
                    Text(problem)
                        .font(.sub)
                        .foregroundStyle(Color.red)
                        .padding(.top, 12)
                        .padding(.horizontal, 4)
                } else if app.session.expired {
                    Text("You were signed out. Sign in again; nothing you logged is lost.")
                        .font(.sub)
                        .foregroundStyle(Color.tx2)
                        .padding(.top, 12)
                        .padding(.horizontal, 4)
                }

                Button {
                    Task { await submit() }
                } label: {
                    if busy { ProgressView().tint(Color.bg) } else { Text(creating ? "Create account" : "Sign in") }
                }
                .buttonStyle(.primary)
                .disabled(busy || username.isEmpty || password.isEmpty)
                .padding(.top, 18)

                Button(creating ? "I have an account" : "Create an account") {
                    withAnimation(.snappy) {
                        creating.toggle()
                        problem = nil
                    }
                }
                .font(.sofia(16, .semibold))
                .foregroundStyle(Color.or)
                .frame(maxWidth: .infinity)
                .padding(.top, 20)
            }
            .padding(.horizontal, 24)
        }
        .scrollDismissesKeyboard(.interactively)
        .overlay(alignment: .bottom) {
            // Stays behind the keyboard rather than riding up over the fields.
            Text("Your log stays on this phone and syncs when you're online.")
                .font(.sofia(12.5, .medium))
                .foregroundStyle(Color.tx3)
                .frame(maxWidth: .infinity)
                .padding(.bottom, 8)
                .ignoresSafeArea(.keyboard)
                .allowsHitTesting(false)
        }
        .miloScreen()
    }

    private func field(_ label: String, text: Binding<String>, field: Field, secure: Bool = false) -> some View {
        HStack(spacing: 0) {
            Text(label)
                .font(.sofia(14, .semibold))
                .foregroundStyle(Color.tx2)
                .frame(width: 84, alignment: .leading)
            Group {
                if secure {
                    SecureField("", text: text)
                } else {
                    TextField("", text: text)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
            }
            .font(.sofia(17, .medium))
            .focused($focus, equals: field)
            .submitLabel(field == .password && !creating ? .go : .next)
            .onSubmit { advance(from: field) }
        }
        .padding(.horizontal, 16)
        .frame(minHeight: 60)
    }

    private func advance(from field: Field) {
        switch field {
        case .username: focus = .password
        case .password: if creating { focus = .invite } else { Task { await submit() } }
        case .invite: Task { await submit() }
        }
    }

    private func submit() async {
        guard !busy, !username.isEmpty, !password.isEmpty else { return }
        busy = true
        problem = nil
        defer { busy = false }
        do {
            let token: String
            let user: Milo_User
            if creating {
                var req = Milo_RegisterRequest()
                req.username = username; req.password = password; req.inviteCode = invite
                let res = try await app.api.auth.register(request: req, headers: [:]).value()
                (token, user) = (res.token, res.user)
            } else {
                var req = Milo_LoginRequest()
                req.username = username; req.password = password
                let res = try await app.api.auth.login(request: req, headers: [:]).value()
                (token, user) = (res.token, res.user)
            }
            try app.session.signedIn(token: token, user: .init(id: user.id, username: user.username))
        } catch {
            problem = error.localizedDescription
        }
    }
}
