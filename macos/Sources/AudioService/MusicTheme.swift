import SwiftUI

enum MusicTheme {
    static let canvas = Color(.sRGB, white: 16 / 255, opacity: 1)
    static let panel = Color(.sRGB, white: 26 / 255, opacity: 1)
    static let raised = Color(.sRGB, white: 46 / 255, opacity: 1)
    static let text = Color(.sRGB, white: 245 / 255, opacity: 1)
    static let muted = Color(.sRGB, white: 179 / 255, opacity: 1)
    static let accent = Color(.sRGB, red: 1, green: 0.8, blue: 0, opacity: 1)
}

struct MusicActionStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .fontWeight(.semibold)
            .padding(.horizontal, 14)
            .padding(.vertical, 7)
            .foregroundStyle(isEnabled ? MusicTheme.canvas : MusicTheme.muted)
            .background(isEnabled ? MusicTheme.accent : MusicTheme.raised, in: Capsule())
            .opacity(configuration.isPressed ? 0.8 : 1)
            .contentShape(Capsule())
    }
}
