package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"timber-kiln-drying-optimizer/backend/internal/constants"
	"timber-kiln-drying-optimizer/backend/internal/dto"
	"timber-kiln-drying-optimizer/backend/internal/model"
	"timber-kiln-drying-optimizer/backend/internal/repository"
)

func testServices(t *testing.T) (context.Context, *model.DryingKiln, *model.TimberLot, LotService, ReadingService, ScheduleService) {
	t.Helper()
	db, err := model.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	now := time.Now().UTC().Add(-time.Hour)
	kiln := &model.DryingKiln{ID: "kiln-" + t.Name(), KilnCode: "K-" + t.Name(), MaxTemperatureC: 70, MinHumidityPct: 35, KilnState: "ready"}
	lot := &model.TimberLot{ID: "lot-" + t.Name(), LotCode: "L-" + t.Name(), KilnID: kiln.ID, Species: "oak", ThicknessMM: 30, VolumeM3: 2, InitialMoisturePct: 45, TargetMoisturePct: 10, LoadedAt: now, LotState: constants.LotQueued, CreatedBy: "creator", Version: 1}
	if err := db.Create(kiln).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(lot).Error; err != nil {
		t.Fatal(err)
	}
	audit := AuditService{Repo: repository.AuditRepository{DB: db}}
	lots := LotService{Repo: repository.LotRepository{DB: db}, Kilns: repository.KilnRepository{DB: db}, Audit: audit}
	readings := ReadingService{Repo: repository.ReadingRepository{DB: db}, Lots: repository.LotRepository{DB: db}, Audit: audit}
	schedules := ScheduleService{Repo: repository.ScheduleRepository{DB: db}, Lots: repository.LotRepository{DB: db}, Kilns: repository.KilnRepository{DB: db}, Readings: repository.ReadingRepository{DB: db}, Audit: audit}
	return context.Background(), kiln, lot, lots, readings, schedules
}

func TestReadingImportValidatesTimestampSamplesAndPhysicalBounds(t *testing.T) {
	ctx, _, lot, _, readings, _ := testServices(t)
	invalid := dto.ReadingImport{TimberLotID: lot.ID, Readings: []dto.ReadingInput{{SamplePosition: "core", MeasuredAt: "not-a-time", MoisturePct: 30, DryBulbC: 50, WetBulbC: 42}}}
	if _, err := readings.Import(ctx, invalid, "analyst", "r1"); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid time error = %v", err)
	}
	measured := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	duplicate := dto.ReadingImport{TimberLotID: lot.ID, Readings: []dto.ReadingInput{{SamplePosition: "core", MeasuredAt: measured, MoisturePct: 30, DryBulbC: 50, WetBulbC: 42}, {SamplePosition: "center", MeasuredAt: measured, MoisturePct: 31, DryBulbC: 50, WetBulbC: 42}}}
	if _, err := readings.Import(ctx, duplicate, "analyst", "r2"); !errors.Is(err, ErrValidation) {
		t.Fatalf("duplicate sample error = %v", err)
	}
	physical := dto.ReadingImport{TimberLotID: lot.ID, Readings: []dto.ReadingInput{{SamplePosition: "surface", MeasuredAt: measured, MoisturePct: 30, DryBulbC: 40, WetBulbC: 45}}}
	if _, err := readings.Import(ctx, physical, "analyst", "r3"); !errors.Is(err, ErrValidation) {
		t.Fatalf("wet bulb error = %v", err)
	}
	valid := dto.ReadingImport{TimberLotID: lot.ID, Readings: []dto.ReadingInput{{SamplePosition: "surface", MeasuredAt: measured, MoisturePct: 30, DryBulbC: 50, WetBulbC: 42}}}
	if created, err := readings.Import(ctx, valid, "analyst", "r4"); err != nil || len(created) != 1 || created[0].SamplePosition != "surface" {
		t.Fatalf("valid import = %+v, %v", created, err)
	}
	if _, err := readings.Import(ctx, valid, "analyst", "r5"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate checksum error = %v", err)
	}
}

func TestLotTransitionUsesVersionAndLegalStateMachine(t *testing.T) {
	ctx, _, lot, lots, _, _ := testServices(t)
	updated, err := lots.Transition(ctx, lot.ID, constants.LotConditioning, "engineer", "l1", 1)
	if err != nil || updated.Version != 2 || updated.LotState != constants.LotConditioning {
		t.Fatalf("valid transition = %+v, %v", updated, err)
	}
	if _, err := lots.Transition(ctx, lot.ID, constants.LotDrying, "engineer", "l2", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale transition error = %v", err)
	}
	if _, err := lots.Transition(ctx, lot.ID, constants.LotCompleted, "engineer", "l3", 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("illegal transition error = %v", err)
	}
}

func TestScheduleCalculationIsIdempotentAndReviewIsVersioned(t *testing.T) {
	ctx, _, lot, lots, readings, schedules := testServices(t)
	transitioned, err := lots.Transition(ctx, lot.ID, constants.LotConditioning, "creator", "s1", 1)
	if err != nil {
		t.Fatal(err)
	}
	lot = &transitioned
	measured := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	input := dto.ReadingImport{TimberLotID: lot.ID, Readings: []dto.ReadingInput{{SamplePosition: "core", MeasuredAt: measured, MoisturePct: 44, DryBulbC: 50, WetBulbC: 44}, {SamplePosition: "surface", MeasuredAt: measured, MoisturePct: 43, DryBulbC: 50, WetBulbC: 44}}}
	if _, err := readings.Import(ctx, input, "creator", "s2"); err != nil {
		t.Fatal(err)
	}
	first, err := schedules.Calculate(ctx, dto.ScheduleCalculate{TimberLotID: lot.ID, IdempotencyKey: "schedule-key"}, "creator", "s3")
	if err != nil || first.ScheduleState != constants.ScheduleProposed {
		t.Fatalf("calculation = %+v, %v", first, err)
	}
	again, err := schedules.Calculate(ctx, dto.ScheduleCalculate{TimberLotID: lot.ID, IdempotencyKey: "schedule-key"}, "creator", "s4")
	if err != nil || again.ID != first.ID {
		t.Fatalf("idempotency = %+v, %v", again, err)
	}
	if _, err := schedules.Review(ctx, first.ID, constants.ScheduleAccepted, "", "creator", "s5", first.Version); !errors.Is(err, ErrForbidden) {
		t.Fatalf("self approval error = %v", err)
	}
	if _, err := schedules.Review(ctx, first.ID, constants.ScheduleAccepted, "checked", "reviewer", "s6", first.Version-1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale review error = %v", err)
	}
	approved, err := schedules.Review(ctx, first.ID, constants.ScheduleAccepted, "checked", "reviewer", "s7", first.Version)
	if err != nil || approved.ScheduleState != constants.ScheduleAccepted || approved.Version != first.Version+1 {
		t.Fatalf("approval = %+v, %v", approved, err)
	}
}

func TestReadingCorrectionAtomicallyVoidsAndReimports(t *testing.T) {
	ctx, _, lot, _, readings, schedules := testServices(t)
	measured := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	input := dto.ReadingImport{TimberLotID: lot.ID, Readings: []dto.ReadingInput{{SamplePosition: "core", MeasuredAt: measured, MoisturePct: 42, DryBulbC: 50, WetBulbC: 44}}}
	created, err := readings.Import(ctx, input, "analyst", "correct-1")
	if err != nil {
		t.Fatal(err)
	}
	replacements, err := readings.Correct(ctx, created[0].ID, input, "analyst", "correct-2", created[0].Version)
	if err != nil || len(replacements) != 1 || replacements[0].SupersedesID != created[0].ID {
		t.Fatalf("correction = %+v, %v", replacements, err)
	}
	original, err := readings.Repo.Get(ctx, created[0].ID)
	if err != nil || original.ReadingQuality != "voided" || original.VoidReason == "" {
		t.Fatalf("original after correction = %+v, %v", original, err)
	}
	accepted, err := readings.Repo.Accepted(ctx, lot.ID)
	if err != nil || len(accepted) != 1 || accepted[0].ID != replacements[0].ID {
		t.Fatalf("active readings = %+v, %v", accepted, err)
	}
	if _, err := schedules.Calculate(ctx, dto.ScheduleCalculate{TimberLotID: lot.ID}, "analyst", "correct-3"); err != nil {
		t.Fatalf("calculation must use corrected active sample: %v", err)
	}
}

func TestScheduleFreezeAndHistoricalComparison(t *testing.T) {
	ctx, _, lot, lots, readings, schedules := testServices(t)
	transitioned, err := lots.Transition(ctx, lot.ID, constants.LotConditioning, "engineer", "history-1", lot.Version)
	if err != nil {
		t.Fatal(err)
	}
	lot = &transitioned
	firstTime := time.Now().UTC().Add(-50 * time.Minute).Format(time.RFC3339)
	firstInput := dto.ReadingImport{TimberLotID: lot.ID, Readings: []dto.ReadingInput{{SamplePosition: "core", MeasuredAt: firstTime, MoisturePct: 46, DryBulbC: 50, WetBulbC: 44}, {SamplePosition: "surface", MeasuredAt: firstTime, MoisturePct: 44, DryBulbC: 50, WetBulbC: 44}}}
	if _, err = readings.Import(ctx, firstInput, "analyst", "history-2"); err != nil {
		t.Fatal(err)
	}
	baseline, err := schedules.Calculate(ctx, dto.ScheduleCalculate{TimberLotID: lot.ID}, "engineer", "history-3")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err = schedules.Review(ctx, baseline.ID, constants.ScheduleAccepted, "approved", "reviewer", "history-4", baseline.Version)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := schedules.Freeze(ctx, baseline.ID, "reviewer", "history-5", baseline.Version)
	if err != nil || frozen.FrozenAt == nil || frozen.FrozenSnapshot == "" {
		t.Fatalf("freeze = %+v, %v", frozen, err)
	}
	if _, err = schedules.Freeze(ctx, baseline.ID, "reviewer", "history-6", frozen.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("second freeze error = %v", err)
	}
	secondTime := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	secondInput := dto.ReadingImport{TimberLotID: lot.ID, Readings: []dto.ReadingInput{{SamplePosition: "core", MeasuredAt: secondTime, MoisturePct: 30, DryBulbC: 51, WetBulbC: 43}, {SamplePosition: "surface", MeasuredAt: secondTime, MoisturePct: 28, DryBulbC: 51, WetBulbC: 43}}}
	if _, err = readings.Import(ctx, secondInput, "analyst", "history-7"); err != nil {
		t.Fatal(err)
	}
	current, err := schedules.Calculate(ctx, dto.ScheduleCalculate{TimberLotID: lot.ID}, "engineer", "history-8")
	if err != nil || current.ID == baseline.ID {
		t.Fatalf("second calculation = %+v, %v", current, err)
	}
	comparison, err := schedules.Compare(ctx, current.ID, baseline.ID, "analyst", "history-9")
	if err != nil || !comparison.FrozenBaseline || comparison.ScheduleID != current.ID {
		t.Fatalf("comparison = %+v, %v", comparison, err)
	}
	other := model.DryingSchedule{ID: "other-schedule-" + t.Name(), TimberLotID: "other-lot", ScheduleState: constants.ScheduleProposed, Version: 1}
	if err = schedules.Repo.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = schedules.Compare(ctx, current.ID, other.ID, "analyst", "history-10"); !errors.Is(err, ErrValidation) {
		t.Fatalf("cross-lot comparison error = %v", err)
	}
}

// importFlagged creates one valid but implausible reading that the import
// assessment marks flagged and pending analyst review.
func importFlagged(t *testing.T, ctx context.Context, lot *model.TimberLot, readings ReadingService, requestID string) model.MoistureReading {
	t.Helper()
	measured := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	input := dto.ReadingImport{TimberLotID: lot.ID, Readings: []dto.ReadingInput{{SamplePosition: "core", MeasuredAt: measured, MoisturePct: 82, DryBulbC: 50, WetBulbC: 50}}}
	created, err := readings.Import(ctx, input, "analyst", requestID)
	if err != nil || len(created) != 1 {
		t.Fatalf("flagged import = %+v, %v", created, err)
	}
	if created[0].ReadingQuality != "flagged" || created[0].ReviewState != constants.ReviewPending {
		t.Fatalf("new reading must be flagged pending review: %+v", created[0])
	}
	return created[0]
}

func TestScheduleBlockedUntilEveryFlaggedReadingIsReviewed(t *testing.T) {
	ctx, _, lot, _, readings, schedules := testServices(t)
	flagged := importFlagged(t, ctx, lot, readings, "flag-gate-1")

	_, err := schedules.Calculate(ctx, dto.ScheduleCalculate{TimberLotID: lot.ID}, "engineer", "flag-gate-2")
	var pending *PendingAnomaliesError
	if !errors.As(err, &pending) {
		t.Fatalf("calculate with pending anomaly error = %v", err)
	}
	if len(pending.Pending) != 1 || pending.Pending[0].ReadingID != flagged.ID || pending.Pending[0].QualityNote == "" {
		t.Fatalf("pending details = %+v", pending.Pending)
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("pending anomaly must map to conflict (409): %v", err)
	}
	if stored, listErr := schedules.Repo.List(ctx); listErr != nil || len(stored) != 0 {
		t.Fatalf("refused simulation must not persist a schedule: %+v, %v", stored, listErr)
	}

	decision := dto.ReadingReview{Decision: constants.ReviewAdopted, Reason: "干球湿球传感器已现场复测，读数可接受", Version: flagged.Version}
	adopted, err := readings.Review(ctx, flagged.ID, decision, "analyst", "flag-gate-3")
	if err != nil || adopted.ReviewState != constants.ReviewAdopted || adopted.Version != flagged.Version+1 || adopted.ReviewedBy != "analyst" {
		t.Fatalf("adopt review = %+v, %v", adopted, err)
	}
	active, err := readings.Repo.Accepted(ctx, lot.ID)
	if err != nil || len(active) != 1 || active[0].ID != flagged.ID {
		t.Fatalf("adopted reading must join calculation: %+v, %v", active, err)
	}
	plan, err := schedules.Calculate(ctx, dto.ScheduleCalculate{TimberLotID: lot.ID}, "engineer", "flag-gate-4")
	if err != nil || plan.ScheduleState != constants.ScheduleProposed {
		t.Fatalf("calculation after adoption = %+v, %v", plan, err)
	}
}

func TestReadingReviewVersionGuardLeavesOnlyFirstCommit(t *testing.T) {
	ctx, _, lot, _, readings, _ := testServices(t)
	flagged := importFlagged(t, ctx, lot, readings, "flag-race-1")

	first := dto.ReadingReview{Decision: constants.ReviewAdopted, Reason: "现场复测确认温度探头偏置，采纳修正后的读数", Version: flagged.Version}
	winner, err := readings.Review(ctx, flagged.ID, first, "analyst-a", "flag-race-2")
	if err != nil || winner.ReviewState != constants.ReviewAdopted {
		t.Fatalf("first review = %+v, %v", winner, err)
	}
	stale := dto.ReadingReview{Decision: constants.ReviewExcluded, Reason: "第二名分析师不应覆盖已完成的采纳结论", Version: flagged.Version}
	if _, err := readings.Review(ctx, flagged.ID, stale, "analyst-b", "flag-race-3"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second analyst must lose the version race: %v", err)
	}
	current, err := readings.Repo.Get(ctx, flagged.ID)
	if err != nil || current.ReviewState != constants.ReviewAdopted || current.ReviewedBy != "analyst-a" {
		t.Fatalf("only the first commit must survive: %+v, %v", current, err)
	}
	invalid := dto.ReadingReview{Decision: constants.ReviewAdopted, Reason: "bad", Version: winner.Version}
	if _, err := readings.Review(ctx, flagged.ID, invalid, "analyst-a", "flag-race-4"); !errors.Is(err, ErrConflict) {
		t.Fatalf("reviewing an already decided reading must conflict: %v", err)
	}
}

func TestExcludedReadingStaysVisibleButLeavesCalculation(t *testing.T) {
	ctx, _, lot, _, readings, schedules := testServices(t)
	flagged := importFlagged(t, ctx, lot, readings, "flag-excl-1")

	decision := dto.ReadingReview{Decision: constants.ReviewExcluded, Reason: "样本位置不属受控采样点，按规程排除该读数", Version: flagged.Version}
	excluded, err := readings.Review(ctx, flagged.ID, decision, "analyst", "flag-excl-2")
	if err != nil || excluded.ReviewState != constants.ReviewExcluded || excluded.ReviewReason == "" {
		t.Fatalf("exclude review = %+v, %v", excluded, err)
	}
	active, err := readings.Repo.Accepted(ctx, lot.ID)
	if err != nil || len(active) != 0 {
		t.Fatalf("excluded reading must not enter calculation: %+v, %v", active, err)
	}
	listed, err := readings.List(ctx, lot.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != flagged.ID {
		t.Fatalf("excluded reading must remain in history listing: %+v, %v", listed, err)
	}
	pending, err := readings.Repo.PendingReview(ctx, lot.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("excluded reading must no longer be pending: %+v, %v", pending, err)
	}
	plan, err := schedules.Calculate(ctx, dto.ScheduleCalculate{TimberLotID: lot.ID}, "engineer", "flag-excl-3")
	if err != nil || plan.ScheduleState != constants.ScheduleProposed {
		t.Fatalf("calculation may proceed on lot initial moisture after exclusion: %+v, %v", plan, err)
	}
}
