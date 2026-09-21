package interactive

import "testing"

func TestJumpAllowed_MatchesCLAUDEmdStateMachineTable(t *testing.T) {
	// CLAUDE.md's "### State Machine": "Кроме последовательного перехода
	// (planning → execution → validation → done) разрешены так же
	// переходы состояний: execution -> planning, validation -> planning,
	// done -> validation, done -> planning."
	allowed := map[[2]string]bool{
		{phaseExecution, phasePlanning}:    true,
		{phaseValidation, phasePlanning}:   true,
		{phaseDone, phaseValidation}:       true,
		{phaseDone, phasePlanning}:         true,
		{phasePlanning, phaseExecution}:    false, // the forward flow isn't a "jump"
		{phasePlanning, phaseValidation}:   false,
		{phasePlanning, phaseDone}:         false,
		{phaseExecution, phaseValidation}:  false,
		{phaseExecution, phaseDone}:        false,
		{phaseValidation, phaseExecution}:  false,
		{phaseValidation, phaseDone}:       false,
		{phaseDone, phaseExecution}:        false,
		{phasePlanning, phasePlanning}:     false,
		{phaseExecution, phaseExecution}:   false,
		{phaseValidation, phaseValidation}: false,
		{phaseDone, phaseDone}:             false,
	}

	for pair, want := range allowed {
		from, to := pair[0], pair[1]
		if got := jumpAllowed(from, to); got != want {
			t.Errorf("jumpAllowed(%q, %q) = %v, want %v", from, to, got, want)
		}
	}
}
