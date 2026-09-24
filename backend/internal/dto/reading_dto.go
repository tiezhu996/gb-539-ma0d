package dto

type ReadingInput struct {
	TimberLotID    string  `json:"timber_lot_id" binding:"required"`
	SamplePosition string  `json:"sample_position" binding:"required"`
	MeasuredAt     string  `json:"measured_at"`
	MoisturePct    float64 `json:"moisture_pct" binding:"gte=0,lte=100"`
	DryBulbC       float64 `json:"dry_bulb_c"`
	WetBulbC       float64 `json:"wet_bulb_c"`
	SupersedesID   string  `json:"supersedes_id"`
}

type ReadingVoid struct {
	Reason  string `json:"reason" binding:"required,min=5,max=500"`
	Version int    `json:"version" binding:"required,gt=0"`
}

// ReadingTriage carries the analyst decision for one flagged reading. The
// version pins the row so only the first of two concurrent decisions lands.
type ReadingTriage struct {
	Decision string `json:"decision" binding:"required,oneof=adopted excluded"`
	Reason   string `json:"reason" binding:"required,min=5,max=500"`
	Version  int    `json:"version" binding:"required,gt=0"`
}
type ReadingImport struct {
	TimberLotID string         `json:"timber_lot_id" binding:"required"`
	Readings    []ReadingInput `json:"readings" binding:"required,min=1"`
}
