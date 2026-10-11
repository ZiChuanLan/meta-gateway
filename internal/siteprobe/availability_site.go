package siteprobe

import (
	"crypto/sha256"
	"fmt"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"time"
)

func sourceIdentity(site domain.Site) string {
	site = resolveProbeSource(site)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(site.ProbeSourceKind+"|"+site.ProbeSourceURL)))
}
func (s *Service) persistHeartbeats(site domain.Site, source *KumaSource) error {
	beats := []store.SiteHeartbeat{}
	cutoff := s.now().Add(-7 * 24 * time.Hour)
	for _, monitor := range source.Monitors {
		for _, beat := range monitor.Heartbeats {
			at := parseKumaTime(beat.Time)
			if at.IsZero() || at.Before(cutoff) || at.After(s.now().Add(time.Minute)) {
				continue
			}
			beats = append(beats, store.SiteHeartbeat{SiteID: site.ID, SourceKey: sourceIdentity(site), MonitorID: monitor.ID,
				RawModel: monitor.Name, GroupName: monitor.GroupName, At: at, Status: beat.Status, PingMS: beat.Ping})
		}
	}
	return s.db.InsertSiteHeartbeats(beats)
}
