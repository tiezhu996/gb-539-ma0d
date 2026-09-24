package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func TestFlaggedReadingBlocksCalculationUntilAnalystTriage(t *testing.T) {
	ctx, _, lot, _, readings, schedules := testServices(t)
	measured := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	flagged := dto.ReadingImport{TimberLotID: lot.ID, Readings: []dto.ReadingInput{{SamplePosition: "core", MeasuredAt: measured, MoisturePct: 3, DryBulbC: 50, WetBulbC: 49.8}}}
	created, err := readings.Import(ctx, flagged, "analyst", "triage-1")
	if err != nil {
		t.Fatal(err)
	}
	if created[0].ReadingQuality != constants.ReadingFlagged {
		t.Fatalf("expected flagged reading, got %+v", created[0])
	}
	if _, err = schedules.Calculate(ctx, dto.ScheduleCalculate{TimberLotID: lot.ID}, "engineer", "triage-2"); err == nil {
		t.Fatal("calculation must refuse lots with pending flagged readings")
	} else {
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("pending triage error = %v", err)
		}
		var pending *PendingTriageError
		if !errors.As(err, &pending) || len(pending.Readings) != 1 || pending.Readings[0].ReadingID != created[0].ID {
			t.Fatalf("affected readings = %+v, %v", pending, err)
		}
		if !strings.Contains(pending.Readings[0].QualityNote, "moisture is at an unusual operating extreme") {
			t.Fatalf("affected reasons = %+v", pending.Readings[0])
		}
	}
	if items, listErr := schedules.List(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("no schedule may be persisted while triage is pending: %+v, %v", items, listErr)
	}
	adopted, err := readings.Triage(ctx, created[0].ID, dto.ReadingTriage{Decision: constants.TriageAdopted, Reason: "仪器复测确认样本有效", Version: created[0].Version}, "analyst", "triage-3")
	if err != nil || adopted.ReadingQuality != constants.ReadingAccepted || adopted.TriagedBy != "analyst" || adopted.TriageReason == "" || adopted.Version != created[0].Version+1 {
		t.Fatalf("adoption = %+v, %v", adopted, err)
	}
	if _, err = schedules.Calculate(ctx, dto.ScheduleCalculate{TimberLotID: lot.ID}, "engineer", "triage-4"); err != nil {
		t.Fatalf("calculation must include adopted sample: %v", err)
	}
}

func TestExcludedReadingStaysOutOfCalculationButRemainsVisible(t *testing.T) {
	ctx, _, lot, _, readings, schedules := testServices(t)
	measured := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	later := time.Now().UTC().Add(-30 * time.Second).Format(time.RFC3339)
	input := dto.ReadingImport{TimberLotID: lot.ID, Readings: []dto.ReadingInput{
		{SamplePosition: "core", MeasuredAt: measured, MoisturePct: 44, DryBulbC: 50, WetBulbC: 44},
		{SamplePosition: "surface", MeasuredAt: measured, MoisturePct: 43, DryBulbC: 50, WetBulbC: 44},
		{SamplePosition: "core", MeasuredAt: later, MoisturePct: 3, DryBulbC: 50, WetBulbC: 49.8},
	}}
	created, err := readings.Import(ctx, input, "analyst", "exclude-1")
	if err != nil {
		t.Fatal(err)
	}
	if created[2].ReadingQuality != constants.ReadingFlagged {
		t.Fatalf("expected flagged reading, got %+v", created[2])
	}
	if _, err = schedules.Calculate(ctx, dto.ScheduleCalculate{TimberLotID: lot.ID}, "engineer", "exclude-2"); err == nil {
		t.Fatal("calculation must refuse lots with pending flagged readings")
	}
	excluded, err := readings.Triage(ctx, created[2].ID, dto.ReadingTriage{Decision: constants.TriageExcluded, Reason: "探头结露导致读数失真", Version: created[2].Version}, "analyst", "exclude-3")
	if err != nil || excluded.ReadingQuality != constants.ReadingExcluded {
		t.Fatalf("exclusion = %+v, %v", excluded, err)
	}
	if _, err = schedules.Calculate(ctx, dto.ScheduleCalculate{TimberLotID: lot.ID}, "engineer", "exclude-4"); err != nil {
		t.Fatalf("calculation must proceed without excluded sample: %v", err)
	}
	accepted, err := readings.Repo.Accepted(ctx, lot.ID)
	if err != nil || len(accepted) != 2 {
		t.Fatalf("excluded sample must not participate: %+v, %v", accepted, err)
	}
	all, err := readings.List(ctx, lot.ID)
	if err != nil || len(all) != 3 {
		t.Fatalf("excluded sample must remain visible as history: %+v, %v", all, err)
	}
}

func TestReadingTriageKeepsOnlyFirstConcurrentDecision(t *testing.T) {
	ctx, _, lot, _, readings, _ := testServices(t)
	measured := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	flagged := dto.ReadingImport{TimberLotID: lot.ID, Readings: []dto.ReadingInput{{SamplePosition: "core", MeasuredAt: measured, MoisturePct: 3, DryBulbC: 50, WetBulbC: 49.8}}}
	created, err := readings.Import(ctx, flagged, "analyst-a", "race-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = readings.Triage(ctx, created[0].ID, dto.ReadingTriage{Decision: constants.TriageAdopted, Reason: "复测确认有效", Version: created[0].Version}, "analyst-a", "race-2"); err != nil {
		t.Fatalf("first decision = %v", err)
	}
	if _, err = readings.Triage(ctx, created[0].ID, dto.ReadingTriage{Decision: constants.TriageExcluded, Reason: "并发处理同一记录", Version: created[0].Version}, "analyst-b", "race-3"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second concurrent decision error = %v", err)
	}
	current, err := readings.Repo.Get(ctx, created[0].ID)
	if err != nil || current.ReadingQuality != constants.ReadingAccepted || current.TriagedBy != "analyst-a" || current.Version != created[0].Version+1 {
		t.Fatalf("only the first decision may persist: %+v, %v", current, err)
	}
	if _, err = readings.Triage(ctx, created[0].ID, dto.ReadingTriage{Decision: constants.TriageExcluded, Reason: "已处理记录再次处理", Version: current.Version}, "analyst-b", "race-4"); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-triage of resolved reading error = %v", err)
	}
	events, err := readings.Audit.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	triaged := 0
	for _, event := range events {
		if event.Action == "triaged" && event.EntityID == created[0].ID {
			triaged++
		}
	}
	if triaged != 1 {
		t.Fatalf("exactly one triage audit event expected, got %d", triaged)
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
