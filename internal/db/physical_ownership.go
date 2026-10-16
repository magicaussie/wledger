package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type PhysicalOwnershipRange struct {
	SessionID    string
	ControllerID int64
	MAC          string
	Output       string
	Start        int64
	Stop         int64
}

type PhysicalOperationMetadata struct {
	SessionID  string
	Function   string
	DurationNS int64
	ExpiresAt  time.Time
}

func (q *Queries) SavePhysicalOperationMetadata(ctx context.Context, m PhysicalOperationMetadata) error {
	_, err := q.db.ExecContext(ctx, `INSERT INTO physical_operation_metadata(session_id,function,duration_ns,expires_at) VALUES(?,?,?,?)`, m.SessionID, m.Function, m.DurationNS, m.ExpiresAt)
	return err
}
func (q *Queries) GetPhysicalOperationMetadata(ctx context.Context, id string) (PhysicalOperationMetadata, error) {
	var m PhysicalOperationMetadata
	err := q.db.QueryRowContext(ctx, `SELECT session_id,function,duration_ns,expires_at FROM physical_operation_metadata WHERE session_id=?`, id).Scan(&m.SessionID, &m.Function, &m.DurationNS, &m.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return m, nil
	}
	return m, err
}
func (q *Queries) SavePhysicalOwnershipRange(ctx context.Context, r PhysicalOwnershipRange) error {
	_, err := q.db.ExecContext(ctx, `INSERT INTO physical_pixel_ownership(session_id,controller_id,mac,output,start_pixel,stop_pixel) VALUES(?,?,?,?,?,?)`, r.SessionID, r.ControllerID, r.MAC, r.Output, r.Start, r.Stop)
	return err
}
func (q *Queries) ListPhysicalOwnershipRanges(ctx context.Context) ([]PhysicalOwnershipRange, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT o.session_id,o.controller_id,o.mac,o.output,o.start_pixel,o.stop_pixel FROM physical_pixel_ownership o JOIN physical_locate_sessions s ON s.session_id=o.session_id WHERE s.state='activating' OR EXISTS (SELECT 1 FROM physical_locate_session_targets t WHERE t.session_id=o.session_id AND t.controller_id=o.controller_id AND t.may_have_received<>0)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []PhysicalOwnershipRange
	for rows.Next() {
		var r PhysicalOwnershipRange
		if err := rows.Scan(&r.SessionID, &r.ControllerID, &r.MAC, &r.Output, &r.Start, &r.Stop); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (q *Queries) PhysicalEmergencyPending(ctx context.Context) (bool, error) {
	var pending bool
	err := q.db.QueryRowContext(ctx, `SELECT active FROM physical_emergency_state WHERE id=1`).Scan(&pending)
	return pending, err
}

// FencePhysicalOperations is called only under the immediate operation claim.
// Persisting stopping even for unsent activations revokes their command authority.
func (q *Queries) FencePhysicalOperations(ctx context.Context) error {
	if _, err := q.db.ExecContext(ctx, `UPDATE physical_emergency_state SET active=1, emergency_id=lower(hex(randomblob(16))) WHERE id=1`); err != nil {
		return err
	}
	// Preserve the pre-target compatibility snapshot too; an unavailable legacy
	// controller must not look resolved merely because its target rows are absent.
	if _, err := q.db.ExecContext(ctx, `INSERT INTO physical_locate_session_targets(session_id,controller_id,controller_address,controller_port,ranges_json,commands_json,fingerprint,may_have_received,last_error) SELECT s.session_id,s.controller_id,s.controller_address,s.controller_port,s.ranges_json,s.commands_json,s.fingerprint,1,s.last_error FROM physical_locate_sessions s WHERE s.state IN ('activating','active','partial','stopping') AND NOT EXISTS (SELECT 1 FROM physical_locate_session_targets t WHERE t.session_id=s.session_id)`); err != nil {
		return err
	}
	// An unattempted but reserved target must also remain owned if its off fails.
	if _, err := q.db.ExecContext(ctx, `UPDATE physical_locate_session_targets SET may_have_received=1 WHERE session_id IN (SELECT session_id FROM physical_locate_sessions WHERE state IN ('activating','active','partial','stopping'))`); err != nil {
		return err
	}

	_, err := q.db.ExecContext(ctx, `UPDATE physical_locate_sessions SET state='stopping' WHERE state IN ('activating','active','partial','stopping') OR EXISTS (SELECT 1 FROM physical_locate_session_targets t WHERE t.session_id=physical_locate_sessions.session_id AND t.may_have_received<>0)`)
	return err
}
func (q *Queries) ResolvePhysicalControllerOff(ctx context.Context, controllerID int64, address string, port int64) error {
	_, err := q.db.ExecContext(ctx, `UPDATE physical_locate_session_targets SET may_have_received=0,last_error='' WHERE controller_id=? AND controller_address=? AND controller_port=?`, controllerID, address, port)
	return err
}
func (q *Queries) RecordPhysicalControllerOffFailure(ctx context.Context, controllerID int64, message string) error {
	_, err := q.db.ExecContext(ctx, `UPDATE physical_locate_session_targets SET last_error=? WHERE controller_id=? AND may_have_received<>0`, message, controllerID)
	return err
}
func (q *Queries) FinishPhysicalEmergencyOff(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx, `UPDATE physical_locate_sessions SET state=CASE WHEN EXISTS (SELECT 1 FROM physical_locate_session_targets t WHERE t.session_id=physical_locate_sessions.session_id AND t.may_have_received<>0) THEN 'partial' ELSE 'stopped' END, last_operation='emergency off attempted',last_error=COALESCE((SELECT group_concat(t.last_error, '; ') FROM physical_locate_session_targets t WHERE t.session_id=physical_locate_sessions.session_id AND t.may_have_received<>0 AND t.last_error<>''),''),last_operation_at=CURRENT_TIMESTAMP WHERE state='stopping'`)
	if err != nil {
		return err
	}
	_, err = q.db.ExecContext(ctx, `UPDATE physical_emergency_state SET active=CASE WHEN EXISTS (SELECT 1 FROM physical_emergency_outcomes WHERE acknowledged=0) OR EXISTS (SELECT 1 FROM physical_locate_session_targets WHERE may_have_received<>0) THEN 1 ELSE 0 END WHERE id=1`)
	return err
}

// PhysicalEmergencyOutcome is the latest durable delivery evidence for one
// exact configured or unresolved historical endpoint. It has no cascading FKs.
type PhysicalEmergencyOutcome struct {
	ControllerID int64
	Address      string
	Port         int64
	EmergencyID  string
	Attempted    bool
	Acknowledged bool
	Error        string
}

func (q *Queries) PhysicalEmergencyID(ctx context.Context) (string, error) {
	var id string
	err := q.db.QueryRowContext(ctx, `SELECT emergency_id FROM physical_emergency_state WHERE id=1`).Scan(&id)
	return id, err
}
func (q *Queries) SavePhysicalEmergencyOutcome(ctx context.Context, r PhysicalEmergencyOutcome) error {
	result, err := q.db.ExecContext(ctx, `INSERT INTO physical_emergency_outcomes(controller_id,controller_address,controller_port,emergency_id,attempted,acknowledged,error) SELECT ?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM physical_emergency_state WHERE id=1 AND active=1 AND emergency_id=?) ON CONFLICT(controller_id,controller_address,controller_port) DO UPDATE SET emergency_id=excluded.emergency_id,attempted=excluded.attempted,acknowledged=excluded.acknowledged,error=excluded.error,updated_at=CURRENT_TIMESTAMP`, r.ControllerID, r.Address, r.Port, r.EmergencyID, r.Attempted, r.Acknowledged, r.Error, r.EmergencyID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("physical emergency outcome generation was superseded")
	}
	return nil
}
func (q *Queries) ListPhysicalEmergencyOutcomes(ctx context.Context) ([]PhysicalEmergencyOutcome, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT controller_id,controller_address,controller_port,emergency_id,attempted,acknowledged,error FROM physical_emergency_outcomes WHERE acknowledged=0 OR emergency_id=(SELECT emergency_id FROM physical_emergency_state WHERE id=1) ORDER BY controller_id,controller_address,controller_port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []PhysicalEmergencyOutcome
	for rows.Next() {
		var r PhysicalEmergencyOutcome
		if err := rows.Scan(&r.ControllerID, &r.Address, &r.Port, &r.EmergencyID, &r.Attempted, &r.Acknowledged, &r.Error); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
