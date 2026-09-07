package adapter

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/varbin"
)

type URLTestHistory struct {
	Time  time.Time `json:"time"`
	Delay uint16    `json:"delay"`
}

type V2RayServer interface {
	LifecycleService
	StatsService() ConnectionTracker
}

type CacheFile interface {
	LifecycleService

	CacheID() string

	StoreFakeIP() bool
	FakeIPStorage

	StoreRDRC() bool
	RDRCStore

	StoreDNS() bool
	DNSCacheStore

	SetDisableExpire(disableExpire bool)
	SetOptimisticTimeout(timeout time.Duration)
	Flush()

	LoadMode() string
	StoreMode(mode string) error
	LoadSelected(group string) string
	StoreSelected(group string, selected string) error
	LoadGroupExpand(group string) (isExpand bool, loaded bool)
	StoreGroupExpand(group string, expand bool) error
	// Rule-set entries are keyed by format and source URL, so identical remote rule-sets are shared
	// across profiles and download transports instead of being cached per cacheID.
	LoadRuleSet(key string) *SavedBinary
	SaveRuleSet(key string, set *SavedBinary) error
}

type SavedBinary struct {
	Content        []byte
	LastUpdated    time.Time
	LastEtag       string
	UpdateInterval time.Duration
}

func (s *SavedBinary) MarshalBinary() ([]byte, error) {
	var buffer bytes.Buffer
	err := binary.Write(&buffer, binary.BigEndian, uint8(2))
	if err != nil {
		return nil, err
	}
	_, err = varbin.WriteUvarint(&buffer, uint64(len(s.Content)))
	if err != nil {
		return nil, err
	}
	_, err = buffer.Write(s.Content)
	if err != nil {
		return nil, err
	}
	err = binary.Write(&buffer, binary.BigEndian, s.LastUpdated.Unix())
	if err != nil {
		return nil, err
	}
	_, err = varbin.WriteUvarint(&buffer, uint64(len(s.LastEtag)))
	if err != nil {
		return nil, err
	}
	_, err = buffer.WriteString(s.LastEtag)
	if err != nil {
		return nil, err
	}
	err = binary.Write(&buffer, binary.BigEndian, int64(s.UpdateInterval))
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (s *SavedBinary) UnmarshalBinary(data []byte) error {
	reader := bytes.NewReader(data)
	var version uint8
	err := binary.Read(reader, binary.BigEndian, &version)
	if err != nil {
		return err
	}
	if version != 1 && version != 2 {
		return E.New("unknown saved binary version: ", version)
	}
	contentLength, err := binary.ReadUvarint(reader)
	if err != nil {
		return err
	}
	if contentLength > uint64(reader.Len()) {
		return E.New("invalid content length: ", contentLength)
	}
	s.Content = make([]byte, contentLength)
	_, err = io.ReadFull(reader, s.Content)
	if err != nil {
		return err
	}
	var lastUpdated int64
	err = binary.Read(reader, binary.BigEndian, &lastUpdated)
	if err != nil {
		return err
	}
	s.LastUpdated = time.Unix(lastUpdated, 0)
	etagLength, err := binary.ReadUvarint(reader)
	if err != nil {
		return err
	}
	if etagLength > uint64(reader.Len()) {
		return E.New("invalid etag length: ", etagLength)
	}
	etagBytes := make([]byte, etagLength)
	_, err = io.ReadFull(reader, etagBytes)
	if err != nil {
		return err
	}
	s.LastEtag = string(etagBytes)
	if version >= 2 {
		var updateInterval int64
		err = binary.Read(reader, binary.BigEndian, &updateInterval)
		if err != nil {
			return err
		}
		s.UpdateInterval = time.Duration(updateInterval)
	}
	return nil
}

type OutboundGroup interface {
	Outbound
	Now() string
	All() []string
}

// OutboundGroupUpdater is implemented by groups whose member snapshot can be
// replaced after a runtime proxy-provider update.
type OutboundGroupUpdater interface {
	OutboundGroup
	UpdateOutbounds(outbounds []string) error
}

type OutboundGroupIcon interface {
	Icon() string
}

type InterruptibleOutboundGroup interface {
	InterruptsExternalConnections() bool
}

type URLTestGroup interface {
	OutboundGroup
	URLTest(ctx context.Context) (map[string]uint16, error)
	PerformUpdateCheck()
}
