import GRDB
import Foundation
import HealthKit
import Observation
import OSLog

/// Apple Health: Milo writes finished workouts (as Traditional Strength Training,
/// with active energy) and reads body weight, height, birth date and sex.
@Observable
final class HealthService {
    private let store = HKHealthStore()
    private let db: AppDatabase
    private(set) var connected: Bool
    private(set) var lastSaved: String?

    // What Milo writes and reads, kept on this phone.
    var writeWorkouts: Bool { didSet { store("writeWorkouts", writeWorkouts) } }
    var writeEnergy: Bool { didSet { store("writeEnergy", writeEnergy) } }
    var readWeight: Bool { didSet { store("readWeight", readWeight) } }
    var readProfile: Bool { didSet { store("readProfile", readProfile) } }
    var readBodyFat: Bool { didSet { store("readBodyFat", readBodyFat) } }

    private static let connectedKey = "health.connected"

    init(db: AppDatabase) {
        self.db = db
        let d = UserDefaults.standard
        connected = d.bool(forKey: Self.connectedKey)
        lastSaved = d.string(forKey: "health.lastSaved")
        func flag(_ key: String, _ fallback: Bool) -> Bool { d.object(forKey: "health." + key) as? Bool ?? fallback }
        writeWorkouts = flag("writeWorkouts", true)
        writeEnergy = flag("writeEnergy", true)
        readWeight = flag("readWeight", true)
        readProfile = flag("readProfile", true)
        readBodyFat = flag("readBodyFat", false)
    }

    private func store(_ key: String, _ value: Bool) {
        UserDefaults.standard.set(value, forKey: "health." + key)
    }

    var available: Bool { HKHealthStore.isHealthDataAvailable() }

    private var writes: Set<HKSampleType> { [HKObjectType.workoutType(), HKQuantityType(.activeEnergyBurned)] }
    private var reads: Set<HKObjectType> {
        [HKQuantityType(.bodyMass), HKQuantityType(.height), HKQuantityType(.bodyFatPercentage),
         HKCharacteristicType(.dateOfBirth), HKCharacteristicType(.biologicalSex), HKObjectType.workoutType()]
    }

    func connect() async {
        guard available else { return }
        do {
            try await store.requestAuthorization(toShare: writes, read: reads)
            connected = true
            UserDefaults.standard.set(true, forKey: Self.connectedKey)
            await importBody()
        } catch {
            log.error("health auth: \(error.localizedDescription, privacy: .public)")
        }
    }

    func disconnect() {
        connected = false
        UserDefaults.standard.set(false, forKey: Self.connectedKey)
    }

    /// Body weight and body fat from the last year, plus height, birth date and sex,
    /// as far as the reads are switched on.
    func importBody() async {
        guard connected else { return }
        let since = Calendar.current.date(byAdding: .year, value: -1, to: .now)!
        var types: [(HKQuantityType, HKUnit)] = []
        if readWeight { types.append((HKQuantityType(.bodyMass), .gramUnit(with: .kilo))) }
        if readBodyFat { types.append((HKQuantityType(.bodyFatPercentage), .percent())) }
        for (type, unit) in types {
            let samples = (try? await quantities(type, since: since)) ?? []
            for s in samples {
                let value = s.quantity.doubleValue(for: unit)
                let isWeight = type == HKQuantityType(.bodyMass)
                do {
                    try db.addBody(weightKg: isWeight ? value : nil, bodyFatPct: isWeight ? nil : value * 100, at: s.endDate, source: "health", id: s.uuid)
                } catch {
                    log.error("health: save reading: \(error.localizedDescription)")
                }
            }
        }
        guard readProfile else { return }
        let before = (try? await db.writer.read { try db.settings($0) }) ?? .fresh
        var settings = before
        if let height = try? await quantities(HKQuantityType(.height), since: .distantPast).last {
            settings.heightCm = height.quantity.doubleValue(for: .meterUnit(with: .centi))
        }
        if let dob = try? store.dateOfBirthComponents(), let date = Calendar.current.date(from: dob) {
            settings.birthDate = date.formatted(.iso8601.year().month().day())
        }
        if let sex = try? store.biologicalSex().biologicalSex {
            settings.sex = switch sex { case .female: "female"; case .male: "male"; case .other: "other"; default: settings.sex }
        }
        if settings != before {
            do {
                try db.save(settings)
            } catch {
                log.error("health: save profile: \(error.localizedDescription)")
            }
        }
    }

    private func quantities(_ type: HKQuantityType, since: Date) async throws -> [HKQuantitySample] {
        let descriptor = HKSampleQueryDescriptor(
            predicates: [.quantitySample(type: type, predicate: HKQuery.predicateForSamples(withStart: since, end: nil))],
            sortDescriptors: [SortDescriptor(\.endDate)])
        return try await descriptor.result(for: store)
    }

    /// Saves a finished workout. Saving again replaces the earlier Health workout.
    func save(workoutID: UUID) async {
        guard connected, writeWorkouts else { return }
        do {
            guard let detail = try await db.writer.read({ try db.workoutDetail($0, id: workoutID) }), let end = detail.workout.endedAt else { return }
            if let old = detail.workout.healthUUID, let existing = try? await workout(old) {
                try await store.delete(existing)
            }
            let builder = HKWorkoutBuilder(healthStore: store, configuration: Self.configuration, device: .local())
            try await builder.beginCollection(at: detail.workout.startedAt)
            let bodyKg = try await db.writer.read { try db.latestBodyKg($0, before: end) } ?? 0
            let shares = detail.entries.map { Maths.ExerciseShare(met: $0.exercise.met, doneSets: $0.doneSets.count) }
            if writeEnergy, let kcal = Maths.activeKcal(durationS: end.timeIntervalSince(detail.workout.startedAt), bodyKg: bodyKg, exercises: shares) {
                let energy = HKQuantitySample(type: HKQuantityType(.activeEnergyBurned), quantity: HKQuantity(unit: .kilocalorie(), doubleValue: kcal), start: detail.workout.startedAt, end: end)
                try await builder.addSamples([energy])
            }
            try await builder.endCollection(at: end)
            try await builder.addMetadata([HKMetadataKeyWorkoutBrandName: "Milo"])
            guard let saved = try await builder.finishWorkout() else { return }
            var w = detail.workout
            w.healthUUID = saved.uuid
            try db.save(w)
            lastSaved = "\(w.name), \(Format.day(w.startedAt))"
            UserDefaults.standard.set(lastSaved, forKey: "health.lastSaved")
        } catch {
            log.error("health save: \(error.localizedDescription, privacy: .public)")
        }
    }

    /// Takes a deleted workout out of Health too.
    func remove(_ healthUUID: UUID?) async {
        guard connected, let healthUUID else { return }
        do {
            if let existing = try await workout(healthUUID) { try await store.delete(existing) }
        } catch {
            log.error("health remove: \(error.localizedDescription, privacy: .public)")
        }
    }

    /// Keeps Health in step with a workout changed away from the workout screen (by a
    /// Milo card, or its undo): one that is now finished is saved if it just finished or
    /// was saved before; one that is gone, or running again, is taken out.
    func follow(before: Workout?, after: Workout?) async {
        func finished(_ w: Workout?) -> Bool { w.map { !$0.deleted && $0.endedAt != nil } ?? false }
        let saved = before?.healthUUID
        if let saved, saved != after?.healthUUID || !finished(after) { await remove(saved) }
        if let after, finished(after), saved != nil || !finished(before) { await save(workoutID: after.id) }
    }

    private static var configuration: HKWorkoutConfiguration {
        let c = HKWorkoutConfiguration()
        c.activityType = .traditionalStrengthTraining
        c.locationType = .indoor
        return c
    }

    private func workout(_ id: UUID) async throws -> HKWorkout? {
        let descriptor = HKSampleQueryDescriptor(predicates: [.workout(HKQuery.predicateForObject(with: id))], sortDescriptors: [])
        return try await descriptor.result(for: store).first
    }
}
