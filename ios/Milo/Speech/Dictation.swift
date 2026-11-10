import AVFoundation
import Observation
import OSLog
import Speech

/// Speech to text for the message fields: tap the mic, talk, and the words land
/// in the field as if typed. Recognition stays on the phone when it can.
@Observable
final class Dictation {
    private(set) var listening = false
    /// Why listening couldn't start, to show once.
    var problem: String?

    @ObservationIgnored private let engine = AVAudioEngine()
    @ObservationIgnored private var request: SFSpeechAudioBufferRecognitionRequest?
    @ObservationIgnored private var task: SFSpeechRecognitionTask?

    /// Starts listening. `heard` gets everything said so far each time it grows.
    func start(heard: @escaping @MainActor (String) -> Void) async {
        guard !listening else { return }
        guard await Self.allowed() else {
            problem = "Milo needs the microphone and speech recognition. Turn them on in Settings, or use the keyboard's own mic."
            return
        }
        guard let recognizer = SFSpeechRecognizer(), recognizer.isAvailable else {
            problem = "Speech recognition isn't available right now."
            return
        }
        do {
            let session = AVAudioSession.sharedInstance()
            try session.setCategory(.record, mode: .measurement, options: .duckOthers)
            try session.setActive(true, options: .notifyOthersOnDeactivation)

            let request = SFSpeechAudioBufferRecognitionRequest()
            request.shouldReportPartialResults = true
            request.addsPunctuation = true
            request.requiresOnDeviceRecognition = recognizer.supportsOnDeviceRecognition
            guard Self.tap(engine.inputNode, into: request) else {
                problem = "No microphone is available."
                return
            }
            engine.prepare()
            try engine.start()
            self.request = request
            task = Self.recognise(request, with: recognizer, heard: heard) { [weak self] in self?.stop() }
            listening = true
        } catch {
            log.error("dictation: \(error.localizedDescription, privacy: .public)")
            problem = "Couldn't start listening."
            stop()
        }
    }

    func stop() {
        guard listening || engine.isRunning else { return }
        engine.stop()
        engine.inputNode.removeTap(onBus: 0)
        request?.endAudio()
        task?.finish()
        request = nil
        task = nil
        listening = false
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }

    // Permission answers, audio buffers and results all arrive on the frameworks'
    // own threads. With the app's code on the main actor by default, a closure
    // written there is expected to run there, and Swift stops the app when it
    // doesn't. So these are built outside the main actor and hop back explicitly.

    nonisolated private static func allowed() async -> Bool {
        let speech = await withCheckedContinuation { done in
            SFSpeechRecognizer.requestAuthorization { done.resume(returning: $0 == .authorized) }
        }
        guard speech else { return false }
        return await AVAudioApplication.requestRecordPermission()
    }

    nonisolated private static func tap(_ input: AVAudioInputNode, into request: SFSpeechAudioBufferRecognitionRequest) -> Bool {
        let format = input.outputFormat(forBus: 0)
        guard format.sampleRate > 0, format.channelCount > 0 else { return false }
        input.installTap(onBus: 0, bufferSize: 1024, format: format) { buffer, _ in request.append(buffer) }
        return true
    }

    nonisolated private static func recognise(
        _ request: SFSpeechAudioBufferRecognitionRequest, with recognizer: SFSpeechRecognizer,
        heard: @escaping @MainActor (String) -> Void, ended: @escaping @MainActor () -> Void
    ) -> SFSpeechRecognitionTask {
        recognizer.recognitionTask(with: request) { result, error in
            let text = result?.bestTranscription.formattedString
            let over = error != nil || result?.isFinal == true
            Task { @MainActor in
                if let text { heard(text) }
                if over { ended() }
            }
        }
    }
}
