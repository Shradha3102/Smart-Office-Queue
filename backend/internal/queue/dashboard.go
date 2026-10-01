package queue

import (
	"context"
	"database/sql"
	"time"

	"smartqueue/internal/auth"
)

// Dashboard returns today's figures. Staff see only their own department; admins see all
// (optionally filtered by deptID > 0).
func (s *Service) Dashboard(ctx context.Context, actor auth.Actor, deptID int) (*Dashboard, error) {
	if !actor.IsAdmin() {
		if actor.DepartmentID == nil {
			return &Dashboard{}, nil
		}
		deptID = *actor.DepartmentID
	}
	depts, err := s.ListDepartments(ctx)
	if err != nil {
		return nil, err
	}

	start, end := s.dayBounds()
	stats := make([]DeptStats, 0, len(depts))
	idx := map[int]int{}
	for _, d := range depts {
		if deptID > 0 && d.ID != deptID {
			continue
		}
		idx[d.ID] = len(stats)
		stats = append(stats, DeptStats{
			DepartmentID: d.ID, Code: d.Code, Name: d.Name, IsPaused: d.IsPaused,
			PauseReason: d.PauseReason, Waiting: d.WaitingCount, NowServing: d.NowServing,
		})
	}

	// Live queue figures.
	rows, err := s.db.QueryContext(ctx, `
		SELECT department_id,
		       COUNT(*) FILTER (WHERE status = 'waiting' AND priority_lane = 1),
		       COUNT(*) FILTER (WHERE status = 'serving'),
		       MAX(called_at) FILTER (WHERE status = 'serving'),
		       COALESCE(MAX(EXTRACT(EPOCH FROM (now() - enqueued_at))) FILTER (WHERE status = 'waiting'), 0)::float8
		FROM tokens WHERE status IN ('waiting','serving') GROUP BY department_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			id, prio, serving int
			since             sql.NullTime
			longest           float64
		)
		if err := rows.Scan(&id, &prio, &serving, &since, &longest); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := idx[id]; ok {
			stats[i].PriorityWaiting = prio
			stats[i].Serving = serving
			stats[i].NowServingSince = nullTime(since)
			stats[i].LongestWaitingMinutes = roundTo(longest/60, 1)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Today's activity, from the event log (attributed to the department where it happened).
	rows, err = s.db.QueryContext(ctx, `
		SELECT department_id,
		       COUNT(*) FILTER (WHERE event_type = 'created'),
		       COUNT(*) FILTER (WHERE event_type = 'called'),
		       COUNT(*) FILTER (WHERE event_type = 'completed'),
		       COUNT(*) FILTER (WHERE event_type IN ('no_show_requeued','no_show_cancelled')),
		       COUNT(*) FILTER (WHERE event_type = 'no_show_cancelled'),
		       COUNT(*) FILTER (WHERE event_type = 'cancelled'),
		       COUNT(*) FILTER (WHERE event_type = 'transferred_out'),
		       COALESCE(AVG((details->>'wait_seconds')::float8) FILTER (WHERE event_type = 'called'), 0),
		       COALESCE(AVG((details->>'service_seconds')::float8) FILTER (WHERE event_type = 'completed'), 0)
		FROM token_events
		WHERE created_at >= $1 AND created_at < $2
		GROUP BY department_id`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                                             int
			issued, called, completed, noShows, autoCancel int
			cancelled, transferred                         int
			avgWait, avgService                            float64
		)
		if err := rows.Scan(&id, &issued, &called, &completed, &noShows, &autoCancel, &cancelled,
			&transferred, &avgWait, &avgService); err != nil {
			return nil, err
		}
		i, ok := idx[id]
		if !ok {
			continue
		}
		st := &stats[i]
		st.Issued, st.Called, st.Completed = issued, called, completed
		st.NoShows, st.AutoCancelledNoShows, st.Cancelled, st.TransferredOut = noShows, autoCancel, cancelled, transferred
		st.AvgWaitMinutes = roundTo(avgWait/60, 1)
		st.AvgServiceMinutes = roundTo(avgService/60, 1)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Totals; averages are weighted by their sample counts.
	var tot DeptStats
	tot.Name = "All departments"
	var waitSum, svcSum float64
	for _, st := range stats {
		tot.Waiting += st.Waiting
		tot.PriorityWaiting += st.PriorityWaiting
		tot.Serving += st.Serving
		tot.Issued += st.Issued
		tot.Called += st.Called
		tot.Completed += st.Completed
		tot.NoShows += st.NoShows
		tot.AutoCancelledNoShows += st.AutoCancelledNoShows
		tot.Cancelled += st.Cancelled
		tot.TransferredOut += st.TransferredOut
		waitSum += st.AvgWaitMinutes * float64(st.Called)
		svcSum += st.AvgServiceMinutes * float64(st.Completed)
		tot.LongestWaitingMinutes = max(tot.LongestWaitingMinutes, st.LongestWaitingMinutes)
	}
	if tot.Called > 0 {
		tot.AvgWaitMinutes = roundTo(waitSum/float64(tot.Called), 1)
	}
	if tot.Completed > 0 {
		tot.AvgServiceMinutes = roundTo(svcSum/float64(tot.Completed), 1)
	}

	return &Dashboard{
		Date:        start.Format("2006-01-02"),
		Timezone:    s.loc.String(),
		GeneratedAt: time.Now().UTC(),
		Totals:      tot,
		Departments: stats,
	}, nil
}
