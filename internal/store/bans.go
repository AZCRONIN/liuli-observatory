package store

import (
	"time"

	"liuliobs/internal/model"
)

// Bans 列出封禁记录。activeOnly 为 true 时只返回当前生效的（未解封、未到期）。
func (s *Store) Bans(activeOnly bool, now time.Time) ([]model.Ban, error) {
	q := `SELECT id, server_id, site, ip, reason, source, created, expires, active, removed FROM bans`
	var args []any
	if activeOnly {
		q += ` WHERE active=1 AND (expires=0 OR expires>?)`
		args = append(args, now.Unix())
	}
	rows, err := s.DB.Query(q+` ORDER BY active DESC, created DESC LIMIT 1000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Ban{}
	for rows.Next() {
		var b model.Ban
		if err := rows.Scan(&b.ID, &b.ServerID, &b.Site, &b.IP, &b.Reason, &b.Source, &b.Created, &b.Expires, &b.Active, &b.Removed); err != nil {
			return nil, err
		}
		if b.Active && b.Expires != 0 && b.Expires <= now.Unix() {
			b.Active, b.Removed = false, b.Expires
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AddBan 新增封禁。同一个 IP 已在封禁中时更新到期时间和原因，不重复插入。
func (s *Store) AddBan(b model.Ban) (int64, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM bans WHERE ip=? AND active=1 AND (expires=0 OR expires>?)`, b.IP, time.Now().Unix()).Scan(&id)
	if err == nil {
		_, err = s.DB.Exec(`UPDATE bans SET reason=?, expires=?, source=? WHERE id=?`, b.Reason, b.Expires, b.Source, id)
		return id, err
	}
	res, err := s.DB.Exec(`INSERT INTO bans(server_id, site, ip, reason, source, created, expires, active) VALUES(?,?,?,?,?,?,?,1)`,
		b.ServerID, b.Site, b.IP, b.Reason, b.Source, b.Created, b.Expires)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Unban 解封。
func (s *Store) Unban(id int64) error {
	_, err := s.DB.Exec(`UPDATE bans SET active=0, removed=? WHERE id=? AND active=1`, time.Now().Unix(), id)
	return err
}

// ExpireBans 把已到期的封禁标记为失效，返回失效的条数。
func (s *Store) ExpireBans(now time.Time) (int64, error) {
	res, err := s.DB.Exec(`UPDATE bans SET active=0, removed=expires WHERE active=1 AND expires>0 AND expires<=?`, now.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// BanHits 统计某个 IP 在封禁期间 [since, until) 被拦截（403）的次数，until 为 0 表示到现在。只看明细保留期内的数据。
func (s *Store) BanHits(ip string, since, until int64) int64 {
	if until == 0 {
		until = time.Now().Unix() + 1
	}
	var n int64
	s.DB.QueryRow(`SELECT COUNT(*) FROM req WHERE ip=? AND status=403 AND ts>=? AND ts<?`, ip, since, until).Scan(&n)
	return n
}
