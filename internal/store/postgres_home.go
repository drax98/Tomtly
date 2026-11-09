package store

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
)

func (p *Postgres) HomeSettings(ctx context.Context) (HomeSettings, error) {
	var h HomeSettings
	err := p.pool.QueryRow(ctx, `SELECT language, timezone, latitude, longitude FROM home_settings WHERE id = 1`).
		Scan(&h.Language, &h.Timezone, &h.Latitude, &h.Longitude)
	if errors.Is(err, pgx.ErrNoRows) {
		return HomeSettings{}, nil
	}
	return h, err
}

func (p *Postgres) SetHomeSettings(ctx context.Context, h HomeSettings) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO home_settings (id, language, timezone, latitude, longitude) VALUES (1, $1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET language = $1, timezone = $2, latitude = $3, longitude = $4`, h.Language, h.Timezone, h.Latitude, h.Longitude)
	return err
}

func (p *Postgres) DevicePins(ctx context.Context) (map[string][]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT device_id, dps FROM device_pins`)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	var id string
	var dps []string
	_, err = pgx.ForEachRow(rows, []any{&id, &dps}, func() error { out[id] = slices.Clone(dps); return nil })
	return out, err
}

func (p *Postgres) SetDevicePins(ctx context.Context, device string, dps []string) error {
	if len(dps) == 0 {
		_, err := p.pool.Exec(ctx, `DELETE FROM device_pins WHERE device_id = $1`, device)
		return err
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO device_pins (device_id, dps) VALUES ($1, $2)
		ON CONFLICT (device_id) DO UPDATE SET dps = $2`, device, dps)
	return err
}

func (p *Postgres) DeviceOverrides(ctx context.Context) ([]DeviceOverride, error) {
	rows, err := p.pool.Query(ctx, `SELECT device_id, name, room FROM device_overrides ORDER BY device_id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[DeviceOverride])
}

func (p *Postgres) SetDeviceOverride(ctx context.Context, o DeviceOverride) error {
	if o.Name == "" && o.Room == nil {
		_, err := p.pool.Exec(ctx, `DELETE FROM device_overrides WHERE device_id = $1`, o.ID)
		return err
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO device_overrides (device_id, name, room) VALUES ($1, $2, $3)
		ON CONFLICT (device_id) DO UPDATE SET name = $2, room = $3`, o.ID, o.Name, o.Room)
	return err
}

func (p *Postgres) Dashboard(ctx context.Context) ([]string, error) {
	var widgets []string
	err := p.pool.QueryRow(ctx, `SELECT widgets FROM dashboard WHERE id = 1`).Scan(&widgets)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err == nil && widgets == nil {
		widgets = []string{}
	}
	return widgets, err
}

func (p *Postgres) SetDashboard(ctx context.Context, widgets []string) error {
	if widgets == nil {
		widgets = []string{} // a jsonb null would read back as never arranged
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO dashboard (id, widgets) VALUES (1, $1) ON CONFLICT (id) DO UPDATE SET widgets = $1`, widgets)
	return err
}

func (p *Postgres) Rooms(ctx context.Context) ([]string, bool, error) {
	var seeded bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM rooms_seeded)`).Scan(&seeded); err != nil {
		return nil, false, err
	}
	rows, err := p.pool.Query(ctx, `SELECT name FROM rooms ORDER BY position, name`)
	if err != nil {
		return nil, false, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	return names, seeded, err
}

func (p *Postgres) SetRooms(ctx context.Context, names []string) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM rooms`); err != nil {
			return err
		}
		for i, name := range names {
			if _, err := tx.Exec(ctx, `INSERT INTO rooms (name, position) VALUES ($1, $2)`, name, i); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO rooms_seeded DEFAULT VALUES ON CONFLICT DO NOTHING`)
		return err
	})
}

func (p *Postgres) RenameRoom(ctx context.Context, from, to string) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE rooms SET name = $2 WHERE name = $1`, from, to)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNoRoom
		}
		_, err = tx.Exec(ctx, `UPDATE device_overrides SET room = $2 WHERE room = $1`, from, to)
		return err
	})
}

func (p *Postgres) DeleteRoom(ctx context.Context, name string) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM rooms WHERE name = $1`, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNoRoom
		}
		_, err = tx.Exec(ctx, `UPDATE device_overrides SET room = '' WHERE room = $1`, name)
		return err
	})
}
