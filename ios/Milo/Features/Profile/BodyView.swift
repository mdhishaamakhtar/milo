import Charts
import GRDB
import SwiftUI

/// Weight over time, body fat, height, birth date and sex. Add or delete a reading;
/// change the details by hand or let Apple Health fill them in.
struct BodyView: View {
    @Environment(AppModel.self) private var app
    @State private var data = Live(BodyData())
    @State private var adding = false
    @State private var editingDetails = false

    struct BodyData: Sendable {
        var readings: [BodyMeasurement] = []
        var settings = UserSettings.fresh
    }

    private var unit: WeightUnit { data.value.settings.displayUnit }
    private var weights: [BodyMeasurement] { data.value.readings.filter { $0.weightKg != nil } }
    private var fromHealth: Bool { app.health.connected && app.health.readProfile }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                weightCard
                details
                Text("Milo uses your weight to estimate calories and to count bodyweight and assisted sets honestly. Values with a heart come from Apple Health.")
                    .font(.sofia(14)).foregroundStyle(Color.tx2).padding(.horizontal, 4)
                if !data.value.readings.isEmpty {
                    SectionHeader("Readings").padding(.top, -12)
                    readings
                }
            }
            .padding(.horizontal, 16)
            .padding(.top, 4)
            .padding(.bottom, 24)
        }
        .miloScreen()
        .navigationTitle("Body")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { adding = true } label: { Image(systemName: "plus") }.accessibilityLabel("Add a reading")
            }
        }
        .sheet(isPresented: $adding) { AddReadingSheet(unit: unit) }
        .sheet(isPresented: $editingDetails) { DetailsSheet(settings: data.value.settings) }
        .onAppear {
            let db = app.db
            data.watch(db) { d in
                BodyData(
                    readings: try BodyMeasurement.filter(Column("deleted") == false).order(Column("measuredAt").desc).fetchAll(d),
                    settings: try db.settings(d))
            }
        }
    }

    // MARK: Weight

    private var weightCard: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Weight").font(.cap).foregroundStyle(Color.tx3)
                    if let latest = weights.first, let kg = latest.weightKg {
                        HStack(alignment: .firstTextBaseline, spacing: 4) {
                            Text(Format.kgIn(kg, unit)).font(.sofia(30, .bold))
                            Text(unit.rawValue).font(.sofia(16, .semibold)).foregroundStyle(Color.tx2)
                        }
                    } else {
                        Text("Not added yet").font(.sofia(20, .semibold))
                    }
                }
                Spacer()
                if let latest = weights.first {
                    Badge(text: Format.shortDay(latest.measuredAt) + (latest.source == "health" ? ", from Health" : ""), color: .tx2)
                }
            }
            let recent = Array(weights.prefix(30).reversed())
            if recent.count > 1 {
                Chart(recent) { m in
                    LineMark(x: .value("Day", m.measuredAt), y: .value("Weight", m.weightKg ?? 0))
                        .foregroundStyle(Color.bl)
                        .lineStyle(StrokeStyle(lineWidth: 2))
                    PointMark(x: .value("Day", m.measuredAt), y: .value("Weight", m.weightKg ?? 0))
                        .foregroundStyle(Color.bl)
                        .symbolSize(18)
                }
                .chartYScale(domain: .automatic(includesZero: false))
                .chartYAxis { AxisMarks(values: .automatic(desiredCount: 3)) { _ in AxisGridLine().foregroundStyle(Color.line) } }
                .chartXAxis {
                    AxisMarks(values: [recent.first!.measuredAt, recent.last!.measuredAt]) { value in
                        AxisValueLabel(anchor: value.index == 0 ? .topLeading : .topTrailing) {
                            if let d = value.as(Date.self) { Text(Format.shortDay(d)) }
                        }
                        .foregroundStyle(Color.tx3)
                        .font(.sofia(12, .medium))
                    }
                }
                .frame(height: 110)
                .padding(.top, 12)
            } else if weights.isEmpty {
                Button("Add your weight") { adding = true }
                    .buttonStyle(.primaryCompact)
                    .padding(.top, 12)
            }
        }
        .card()
    }

    // MARK: Details

    private var details: some View {
        let s = data.value.settings
        let fat = data.value.readings.first { $0.bodyFatPct != nil }
        return VStack(spacing: 0) {
            detailRow("Body fat", fat?.bodyFatPct.map { "\(Format.trim(String(format: "%.1f", $0)))%" }, heart: fat?.source == "health") { adding = true }
            Divider().overlay(Color.line).padding(.leading, 16)
            detailRow("Height", s.heightCm.map { "\(Int($0.rounded())) cm" }, heart: fromHealth && s.heightCm != nil) { editingDetails = true }
            Divider().overlay(Color.line).padding(.leading, 16)
            detailRow("Date of birth", birthDate(s.birthDate), heart: fromHealth && !s.birthDate.isEmpty) { editingDetails = true }
            Divider().overlay(Color.line).padding(.leading, 16)
            detailRow("Sex", s.sex.isEmpty ? nil : s.sex.capitalized, heart: fromHealth && !s.sex.isEmpty) { editingDetails = true }
        }
        .background(Color.s1, in: .rect(cornerRadius: Radius.card))
        .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
    }

    private func detailRow(_ title: String, _ value: String?, heart: Bool, edit: @escaping () -> Void) -> some View {
        Button(action: edit) {
            HStack {
                Text(title).font(.sofia(16, .semibold))
                Spacer()
                Text(value ?? "Add").font(.sofia(15)).foregroundStyle(value == nil ? Color.or : Color.tx2)
                if heart {
                    Image(systemName: "heart").font(.system(size: 12, weight: .semibold)).foregroundStyle(Color.or)
                        .accessibilityLabel("From Apple Health")
                }
            }
            .padding(.horizontal, 16)
            .frame(minHeight: 54)
            .contentShape(.rect)
        }
        .buttonStyle(.row)
    }

    /// "12 Mar 2001" from the stored "2001-03-12".
    private func birthDate(_ iso: String) -> String? {
        guard let d = try? Date(iso + "T12:00:00Z", strategy: .iso8601) else { return nil }
        return d.formatted(.dateTime.day().month(.abbreviated).year())
    }

    // MARK: Readings

    private var readings: some View {
        VStack(spacing: 0) {
            ForEach(Array(data.value.readings.prefix(60).enumerated()), id: \.element.id) { i, m in
                if i > 0 { Divider().overlay(Color.line).padding(.leading, 16) }
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(readingText(m)).font(.sofia(16, .semibold))
                        Text(m.measuredAt.formatted(.dateTime.day().month(.abbreviated).year()) + (m.source == "health" ? ", from Health" : ""))
                            .font(.sofia(14)).foregroundStyle(Color.tx2)
                    }
                    Spacer()
                }
                .padding(.horizontal, 16)
                .frame(minHeight: 58)
                .background(Color.s1)
                .swipeToDelete("Delete reading", cornerRadius: 0) { delete(m) }
                .contextMenu {
                    Button("Delete reading", systemImage: "trash", role: .destructive) { delete(m) }
                }
            }
        }
        .background(Color.s1)
        .clipShape(.rect(cornerRadius: Radius.card))
        .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
    }

    private func readingText(_ m: BodyMeasurement) -> String {
        var parts: [String] = []
        if let kg = m.weightKg { parts.append("\(Format.kgIn(kg, unit)) \(unit.rawValue)") }
        if let fat = m.bodyFatPct { parts.append("\(Format.trim(String(format: "%.1f", fat)))% body fat") }
        return parts.joined(separator: ", ")
    }

    private func delete(_ m: BodyMeasurement) {
        try? app.db.deleteBody(m.id)
        app.edited()
    }
}

/// A new weight and, if you know it, body fat.
private struct AddReadingSheet: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let unit: WeightUnit
    @State private var weight = ""
    @State private var fat = ""
    @State private var date = Date.now
    @FocusState private var focused: Bool

    private var weightKg: Double? { Maths.toKg(weight.replacingOccurrences(of: ",", with: "."), unit) }
    private var fatPct: Double? { Double(fat.replacingOccurrences(of: ",", with: ".")) }

    var body: some View {
        NavigationStack {
            MiloForm {
                Section {
                    HStack {
                        TextField("Weight", text: $weight).keyboardType(.decimalPad).focused($focused)
                        Text(unit.rawValue).foregroundStyle(Color.tx2)
                    }
                    .alignmentGuide(.listRowSeparatorLeading) { _ in 0 }
                    HStack {
                        TextField("Body fat (optional)", text: $fat).keyboardType(.decimalPad)
                        Text("%").foregroundStyle(Color.tx2)
                    }
                    .alignmentGuide(.listRowSeparatorLeading) { _ in 0 }
                    DatePicker("When", selection: $date, in: ...Date.now)
                }
            }
            .navigationTitle("New reading")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                SheetToolbar(canSave: weightKg != nil || fatPct != nil, cancel: { dismiss() }) {
                    try? app.db.addBody(weightKg: weightKg, bodyFatPct: fatPct, at: date, source: "manual")
                    app.edited()
                    dismiss()
                }
            }
            .onAppear { focused = true }
        }
        .presentationDetents([.medium])
    }
}

/// Height, birth date and sex, for better calorie estimates.
private struct DetailsSheet: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let settings: UserSettings
    @State private var height = ""
    @State private var hasBirthDate = false
    @State private var birthDate = Calendar.current.date(byAdding: .year, value: -30, to: .now)!
    @State private var sex = ""

    var body: some View {
        NavigationStack {
            MiloForm {
                Section {
                    HStack {
                        TextField("Height", text: $height).keyboardType(.numberPad)
                        Text("cm").foregroundStyle(Color.tx2)
                    }
                    .alignmentGuide(.listRowSeparatorLeading) { _ in 0 }
                    Toggle("Date of birth", isOn: $hasBirthDate.animation()).tint(.or)
                    if hasBirthDate {
                        DatePicker("Born", selection: $birthDate, in: ...Date.now, displayedComponents: .date)
                    }
                    Picker("Sex", selection: $sex) {
                        Text("Not set").tag("")
                        Text("Female").tag("female")
                        Text("Male").tag("male")
                        Text("Other").tag("other")
                    }
                } footer: {
                    Text("Used only to estimate calories. Apple Health fills these in when it's connected.")
                }
            }
            .navigationTitle("Details")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { SheetToolbar(cancel: { dismiss() }, save: save) }
            .onAppear {
                height = settings.heightCm.map { String(Int($0.rounded())) } ?? ""
                sex = settings.sex
                if let d = try? Date(settings.birthDate + "T12:00:00Z", strategy: .iso8601) {
                    hasBirthDate = true
                    birthDate = d
                }
            }
        }
    }

    private func save() {
        var s = settings
        s.heightCm = Double(height).flatMap { (50...260).contains($0) ? $0 : nil }
        s.birthDate = hasBirthDate ? birthDate.formatted(.iso8601.year().month().day()) : ""
        s.sex = sex
        try? app.db.save(s)
        app.edited()
        dismiss()
    }
}
