import MiloAPI
import SwiftUI

/// The first screen. It says what Milo is and plays a few moments of talking to it,
/// with signing in (or creating an account) underneath.
struct SignInView: View {
    @Environment(AppModel.self) private var app
    @State private var creating = false
    @State private var username = ""
    @State private var password = ""
    @State private var invite = ""
    @State private var showPassword = false
    @State private var busy = false
    @State private var problem: String?
    @FocusState private var focus: Field?

    private enum Field { case username, password, invite }

    var body: some View {
        // While typing (and signing in), the pitch steps aside so the form sits just above the keyboard.
        let typing = focus != nil || busy
        GeometryReader { geo in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    MiloWordmark(working: busy)
                        .padding(.top, 14)

                    Text("The workout log\nyou can talk to.")
                        .font(.sofia(33, .bold, relativeTo: .largeTitle))
                        .tracking(-0.7)
                        .lineSpacing(1)
                        .padding(.top, 30)

                    if !typing {
                        Text("Tap in your sets as you always have, or just say them. Milo logs them, learns movements it has never seen, and knows your whole history.")
                            .font(.sofia(16.5, .regular, relativeTo: .body))
                            .foregroundStyle(Color.tx2)
                            .lineSpacing(3)
                            .padding(.top, 14)
                            .transition(.opacity)
                        Spacer(minLength: 30)
                        MiloDemo()
                            .transition(.opacity)
                    }

                    Spacer(minLength: 30)
                    form
                }
                .padding(.horizontal, 24)
                .padding(.bottom, 10)
                .frame(minHeight: geo.size.height, alignment: .top)
            }
            .scrollBounceBehavior(.basedOnSize)
            .scrollDismissesKeyboard(.interactively)
        }
        .animation(.settle, value: typing)
        .miloScreen()
    }

    private var form: some View {
        VStack(alignment: .leading, spacing: 10) {
            well("person", .username) {
                TextField("", text: $username, prompt: prompt("Username"))
                    .textContentType(.username)
                    .modifier(FieldText())
                    .focused($focus, equals: .username)
                    .submitLabel(.next)
                    .onSubmit { focus = .password }
                    .padding(.trailing, 16)
            }
            well("lock", .password) {
                Group {
                    if showPassword {
                        TextField("", text: $password, prompt: prompt("Password"))
                    } else {
                        SecureField("", text: $password, prompt: prompt("Password"))
                    }
                }
                .textContentType(creating ? .newPassword : .password)
                .modifier(FieldText())
                .focused($focus, equals: .password)
                .submitLabel(creating ? .next : .go)
                .onSubmit { if creating { focus = .invite } else { Task { await submit() } } }
                Button {
                    showPassword.toggle()
                    focus = .password
                } label: {
                    Image(systemName: showPassword ? "eye.slash" : "eye")
                        .font(.system(size: 15, weight: .medium))
                        .foregroundStyle(Color.tx2)
                        .contentTransition(.symbolEffect(.replace))
                        .frame(width: 50, height: 54)
                }
                .accessibilityLabel(showPassword ? "Hide password" : "Show password")
            }
            if creating {
                well("ticket", .invite) {
                    TextField("", text: $invite, prompt: prompt("Invite code"))
                        .modifier(FieldText())
                        .focused($focus, equals: .invite)
                        .submitLabel(.go)
                        .onSubmit { Task { await submit() } }
                        .padding(.trailing, 16)
                }
                .transition(.arrive)
            }

            note
                .padding(.horizontal, 4)

            Button {
                Task { await submit() }
            } label: {
                if busy { ProgressView().tint(Color.bg) } else { Text(creating ? "Create account" : "Sign in") }
            }
            .buttonStyle(.primary)
            .disabled(busy || username.isEmpty || password.isEmpty)
            .padding(.top, 6)

            Button {
                withAnimation(.settle) {
                    creating.toggle()
                    problem = nil
                }
            } label: {
                Text("\(Text(creating ? "Have an account?" : "New to Milo?").foregroundStyle(Color.tx2)) \(Text(creating ? "Sign in" : "Create an account").foregroundStyle(Color.or))")
                    .font(.sofia(15.5, .semibold, relativeTo: .subheadline))
                    .frame(maxWidth: .infinity, minHeight: 44)
                    .contentShape(.rect)
            }
            .buttonStyle(.row)
        }
    }

    /// The one line between the fields and the button: a problem, a sign-out notice, or what sign-up needs.
    @ViewBuilder private var note: some View {
        if let problem {
            Label(problem, systemImage: "exclamationmark.circle")
                .font(.sub)
                .foregroundStyle(Color.red)
        } else if app.session.expired && !creating {
            Text("You were signed out. Sign in again; nothing you logged is lost.")
                .font(.sub)
                .foregroundStyle(Color.tx2)
        } else if creating {
            Text("Milo is invite-only for now.")
                .font(.sub)
                .foregroundStyle(Color.tx2)
        }
    }

    /// A text well: an icon, the field, and an orange edge while it has focus.
    private func well<Content: View>(_ icon: String, _ field: Field, @ViewBuilder _ content: () -> Content) -> some View {
        let focused = focus == field
        return HStack(spacing: 12) {
            Image(systemName: icon)
                .font(.system(size: 15, weight: .medium))
                .foregroundStyle(focused ? Color.tx : Color.tx2)
                .frame(width: 20)
            content()
        }
        .padding(.leading, 16)
        .frame(minHeight: 54)
        .background(Color.s1, in: .rect(cornerRadius: Radius.well))
        .overlay(RoundedRectangle(cornerRadius: Radius.well).strokeBorder(focused ? Color.or.opacity(0.75) : Color.line2))
        .contentShape(.rect)
        .onTapGesture { focus = field }
        .animation(.quick, value: focused)
    }

    private func prompt(_ text: String) -> Text {
        Text(text).foregroundStyle(Color.tx2)
    }

    private func submit() async {
        guard !busy, !username.isEmpty, !password.isEmpty else { return }
        // The keyboard goes as soon as you submit, not when the next screen arrives.
        focus = nil
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
            withAnimation(.settle) { problem = error.localizedDescription }
        }
    }
}

/// How typed text looks in a sign-in well.
private struct FieldText: ViewModifier {
    func body(content: Content) -> some View {
        content
            .font(.sofia(17, .medium))
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()
            .tint(Color.or)
    }
}

/// A few moments of talking to Milo, played in turn: what you said, Milo thinking, what it did.
/// The tabs say what each one shows and jump to it.
private struct MiloDemo: View {
    private enum Answer {
        case card(icon: String, title: String, detail: String)
        case reply(String)
    }

    private struct Moment {
        let tab: String
        let said: String
        let answer: Answer
    }

    private static let moments = [
        Moment(tab: "Log it", said: "Lat pulldown, 55 for 10 and 10",
               answer: .card(icon: "checkmark", title: "Logged 2 sets of Lat Pulldown", detail: "55 kg × 10 · 55 kg × 10")),
        Moment(tab: "Teach it", said: "Landmine rotation, bar in the corner, 20 kg, 12 each side",
               answer: .card(icon: "plus", title: "New exercise: Landmine Rotation", detail: "Core and obliques · counted per side")),
        Moment(tab: "Fix it", said: "That last set was 12, not 10",
               answer: .card(icon: "arrow.left.arrow.right", title: "Set 2 of Lat Pulldown", detail: "10 reps → 12 reps · Undo any time")),
        Moment(tab: "Ask it", said: "What have I been skipping lately?",
               answer: .reply("Hamstrings. Your last Lying Leg Curl was 19 days ago, while chest got two sessions a week.")),
    ]

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Namespace private var tabs
    @State private var index = 0
    /// 0: just what you said; 1: Milo thinking; 2: Milo's answer.
    @State private var phase = 0

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 20) {
                ForEach(Self.moments.indices, id: \.self) { i in
                    Button { show(i) } label: {
                        Text(Self.moments[i].tab)
                            .font(.sofia(15, .semibold, relativeTo: .subheadline))
                            .foregroundStyle(i == index ? Color.tx : Color.tx2)
                            .padding(.vertical, 10)
                            .overlay(alignment: .bottom) {
                                if i == index {
                                    Capsule().fill(Color.or).frame(height: 2)
                                        .matchedGeometryEffect(id: "tab", in: tabs)
                                }
                            }
                            .frame(minHeight: 44)
                            .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .accessibilityAddTraits(i == index ? .isSelected : [])
                }
            }

            ZStack(alignment: .topLeading) {
                moment(Self.moments[index])
                    .id(index)
                    // The old moment clears before the next one arrives, so they never overlap.
                    .transition(reduceMotion ? .opacity : .asymmetric(
                        insertion: .opacity.combined(with: .offset(y: 6)).animation(.settle.delay(0.2)),
                        removal: .opacity.animation(.easeOut(duration: 0.2))
                    ))
            }
            .frame(maxWidth: .infinity, minHeight: 190, alignment: .topLeading)
        }
        .task(id: index) { await play() }
    }

    private func moment(_ m: Moment) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            UserBubble(text: m.said)
            if phase >= 1 {
                MiloLabel(working: phase == 1).transition(.opacity)
            }
            if phase >= 2 {
                answer(m.answer).transition(.arrive)
            }
        }
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder private func answer(_ a: Answer) -> some View {
        switch a {
        case .card(let icon, let title, let detail):
            HStack(spacing: 12) {
                Image(systemName: icon)
                    .font(.system(size: 13, weight: .bold))
                    .foregroundStyle(Color.or)
                    .frame(width: 30, height: 30)
                    .background(Color.or.opacity(0.18), in: .circle)
                VStack(alignment: .leading, spacing: Space.rowText) {
                    Text(title).font(.sofia(16, .semibold))
                    Text(detail).font(.sofia(13.5, .regular)).foregroundStyle(Color.tx2)
                }
                Spacer(minLength: 0)
            }
            .padding(14)
            .background(Color.s1, in: .rect(cornerRadius: 22))
            .overlay(RoundedRectangle(cornerRadius: 22).strokeBorder(Color.line))
        case .reply(let text):
            Text(text).font(.body16).lineSpacing(3).padding(.horizontal, 4)
        }
    }

    /// Plays the current moment, holds it, then moves on to the next.
    private func play() async {
        do {
            if reduceMotion {
                phase = 2
            } else if phase < 2 {
                try await Task.sleep(for: .milliseconds(550))
                withAnimation(.settle) { phase = 1 }
                try await Task.sleep(for: .milliseconds(1100))
                withAnimation(.settle) { phase = 2 }
            }
            try await Task.sleep(for: .seconds(3.6))
            show((index + 1) % Self.moments.count)
        } catch {}
    }

    private func show(_ i: Int) {
        guard i != index else { return }
        withAnimation(.settle) {
            index = i
            phase = reduceMotion ? 2 : 0
        }
    }
}
