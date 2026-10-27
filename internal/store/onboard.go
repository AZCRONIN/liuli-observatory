package store

import "time"

// OnboardBackup 是「一键接入」修改网站配置前保存的原始内容，用于撤销。
type OnboardBackup struct {
	SiteID  uint   `json:"site_id"`
	Domain  string `json:"domain"`
	Content string `json:"-"`
	Created int64  `json:"created"`
}

const onboardSchema = `CREATE TABLE IF NOT EXISTS onboard_backups(
	site_id INTEGER PRIMARY KEY, domain TEXT NOT NULL, content TEXT NOT NULL, created INTEGER NOT NULL)`

// SaveOnboardBackup 保存网站配置的原始内容。已有备份时不覆盖，保证撤销能回到观测台改动之前的样子。
func (s *Store) SaveOnboardBackup(siteID uint, domain, content string) error {
	_, err := s.DB.Exec(`INSERT OR IGNORE INTO onboard_backups(site_id, domain, content, created) VALUES(?,?,?,?)`,
		siteID, domain, content, time.Now().Unix())
	return err
}

// OnboardBackup 读取备份，没有时 ok 为 false。
func (s *Store) OnboardBackup(siteID uint) (OnboardBackup, bool) {
	var b OnboardBackup
	err := s.DB.QueryRow(`SELECT site_id, domain, content, created FROM onboard_backups WHERE site_id=?`, siteID).
		Scan(&b.SiteID, &b.Domain, &b.Content, &b.Created)
	return b, err == nil
}

// OnboardBackupIDs 返回有备份的网站 ID。
func (s *Store) OnboardBackupIDs() map[uint]bool {
	out := map[uint]bool{}
	rows, err := s.DB.Query(`SELECT site_id FROM onboard_backups`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id uint
		rows.Scan(&id)
		out[id] = true
	}
	return out
}

// DeleteOnboardBackup 删除备份（撤销完成后）。
func (s *Store) DeleteOnboardBackup(siteID uint) error {
	_, err := s.DB.Exec(`DELETE FROM onboard_backups WHERE site_id=?`, siteID)
	return err
}
