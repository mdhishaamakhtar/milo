import SwiftUI
import UIKit

extension View {
    /// Swipe left to show a red Delete; a long swipe deletes straight away.
    /// For rows inside cards and scroll views, where `.swipeActions` doesn't work.
    func swipeToDelete(_ label: String = "Delete", cornerRadius: CGFloat = Radius.well, action: @escaping () -> Void) -> some View {
        modifier(SwipeToDelete(label: label, cornerRadius: cornerRadius, action: action))
    }
}

private struct SwipeToDelete: ViewModifier {
    let label: String
    let cornerRadius: CGFloat
    let action: () -> Void
    @State private var offset: CGFloat = 0
    /// Where the row was when the drag began.
    @State private var start: CGFloat = 0
    @State private var width: CGFloat = 0
    /// Where the row rests while Delete is showing.
    private let reveal: CGFloat = 76

    func body(content: Content) -> some View {
        content
            .overlay {
                // While open, a tap on the row closes it rather than editing. It slides
                // with the row, so the revealed Delete stays tappable.
                if offset < 0 { Color.clear.contentShape(.rect).onTapGesture { settle(0) } }
            }
            .offset(x: offset)
            .background(alignment: .trailing) {
                if offset < 0 {
                    Button(role: .destructive, action: delete) {
                        Image(systemName: "trash")
                            .font(.system(size: 16, weight: .semibold))
                            .foregroundStyle(.white)
                            .frame(width: max(-offset - 6, 0))
                            .frame(maxHeight: .infinity)
                            .background(Color.red, in: .rect(cornerRadius: cornerRadius))
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(label)
                }
            }
            // The row slides under its own edge, not out of the card.
            .clipShape(.rect)
            .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
            .gesture(SidewaysPan(
                began: { start = offset },
                changed: { dx in offset = min(0, start + dx) },
                ended: { dx, velocity in
                    let x = min(0, start + dx)
                    if x < -width * 0.55 || velocity < -1400 { delete() }
                    else if x < -reveal / 2 || velocity < -400 { settle(-reveal) }
                    else { settle(0) }
                }))
            .accessibilityAction(named: label, action)
    }

    private func settle(_ x: CGFloat) {
        withAnimation(.snappy(duration: 0.25)) { offset = x }
    }

    private func delete() {
        UIImpactFeedbackGenerator(style: .medium).impactOccurred()
        offset = 0
        withAnimation(.snappy(duration: 0.22)) { action() }
    }
}

/// A pan that only begins when the finger moves sideways, so vertical scrolling is untouched.
private struct SidewaysPan: UIGestureRecognizerRepresentable {
    let began: () -> Void
    let changed: (CGFloat) -> Void
    let ended: (_ dx: CGFloat, _ velocity: CGFloat) -> Void

    func makeCoordinator(converter: CoordinateSpaceConverter) -> Coordinator { Coordinator() }

    func makeUIGestureRecognizer(context: Context) -> UIPanGestureRecognizer {
        let pan = UIPanGestureRecognizer()
        pan.delegate = context.coordinator
        return pan
    }

    func handleUIGestureRecognizerAction(_ pan: UIPanGestureRecognizer, context: Context) {
        let dx = pan.translation(in: pan.view).x
        switch pan.state {
        case .began: began()
        case .changed: changed(dx)
        case .ended, .cancelled: ended(dx, pan.velocity(in: pan.view).x)
        default: break
        }
    }

    final class Coordinator: NSObject, UIGestureRecognizerDelegate {
        func gestureRecognizerShouldBegin(_ g: UIGestureRecognizer) -> Bool {
            guard let pan = g as? UIPanGestureRecognizer else { return false }
            let v = pan.velocity(in: pan.view)
            return abs(v.x) > abs(v.y) * 1.3
        }
    }
}
