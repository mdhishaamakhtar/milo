import SwiftUI

/// The mic in a message field. Tap to talk; what's said is added to the text.
/// Tap again, or send, to stop.
struct MicButton: View {
    @Binding var text: String
    @State private var dictation = Dictation()
    /// What the field held when listening began; speech is added after it.
    @State private var typed = ""

    var body: some View {
        Button(action: toggle) {
            Image(systemName: dictation.listening ? "waveform" : "mic")
                .font(.system(size: 17, weight: .medium))
                .foregroundStyle(dictation.listening ? Color.or : Color.tx2)
                .symbolEffect(.variableColor.iterative, isActive: dictation.listening)
                .contentTransition(.symbolEffect(.replace))
                .frame(width: 38, height: 38)
                .contentShape(.circle)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(dictation.listening ? "Stop dictating" : "Dictate")
        .sensoryFeedback(.impact(weight: .light), trigger: dictation.listening)
        .onChange(of: text) { _, now in
            // The message was sent, or cleared by hand.
            if now.isEmpty { dictation.stop(discardingLateResults: true) }
        }
        .onDisappear { dictation.stop(discardingLateResults: true) }
        .alert("Can't listen", isPresented: Binding(get: { dictation.problem != nil }, set: { if !$0 { dictation.problem = nil } })) {
            Button("OK") {}
        } message: {
            Text(dictation.problem ?? "")
        }
    }

    private func toggle() {
        if dictation.listening {
            dictation.stop()
            return
        }
        typed = text
        Task {
            await dictation.start { said in
                text = typed.isEmpty ? said : typed + " " + said
            }
        }
    }
}
