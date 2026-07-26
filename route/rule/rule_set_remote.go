package rule

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
	"github.com/sagernet/sing/service/pause"

	"go4.org/netipx"
)

var _ adapter.RuleSet = (*RemoteRuleSet)(nil)

// ruleSetFetchTimeout bounds a single fetch attempt, including the response body. Without it a
// source that accepts the connection and then stalls would hold up the whole start, and the
// fallback chain would never be reached. Generous enough for the largest rule-sets in common use
// (a few hundred KB) over a slow link.
const ruleSetFetchTimeout = 20 * time.Second

// ruleSetFallbackBudget bounds the fallback chain as a whole. Since rule-set initialization no
// longer fails fast, every rule-set that cannot reach its configured source walks its fallbacks, and
// bounding only each attempt would let one slow network turn into minutes of startup with no
// working route. One rule-set therefore costs at most ruleSetFetchTimeout + this.
const ruleSetFallbackBudget = 30 * time.Second

// ruleSetRetryInterval is how soon a rule-set that has never loaded is tried again, instead of
// waiting out its configured update interval. See RemoteRuleSet.nextUpdateDelay.
const ruleSetRetryInterval = 10 * time.Minute

type RemoteRuleSet struct {
	ctx            context.Context
	cancel         context.CancelFunc
	logger         logger.ContextLogger
	outbound       adapter.OutboundManager
	tag            string
	url            string
	cacheKey       string
	initialPath    string
	options        option.RuleSet
	updateInterval time.Duration
	httpClient     *http.Client
	access         sync.RWMutex
	rules          []adapter.HeadlessRule
	metadata       adapter.RuleSetMetadata
	lastUpdated    time.Time
	lastEtag       string
	lastEtagURL    string
	directAccess   sync.Mutex
	directClient   *http.Client
	cacheFile      adapter.CacheFile
	pauseManager   pause.Manager
	callbacks      list.List[adapter.RuleSetUpdateCallback]
	refs           atomic.Int32
}

func NewRemoteRuleSet(ctx context.Context, logger logger.ContextLogger, tag string, options option.RuleSet) (*RemoteRuleSet, error) {
	ctx, cancel := context.WithCancel(ctx)
	var updateInterval time.Duration
	if options.RemoteOptions.UpdateInterval > 0 {
		updateInterval = time.Duration(options.RemoteOptions.UpdateInterval)
	} else {
		updateInterval = 24 * time.Hour
	}
	var initialPath string
	if options.RemoteOptions.InitialPath != "" {
		initialPath = filemanager.BasePath(ctx, strings.ReplaceAll(options.RemoteOptions.InitialPath, C.RuleSetTagPlaceholder, tag))
		initialPath, _ = filepath.Abs(initialPath)
	}
	url := strings.ReplaceAll(options.RemoteOptions.URL, C.RuleSetTagPlaceholder, tag)
	return &RemoteRuleSet{
		ctx:            ctx,
		cancel:         cancel,
		outbound:       service.FromContext[adapter.OutboundManager](ctx),
		logger:         logger,
		tag:            tag,
		url:            url,
		cacheKey:       ruleSetCacheKey(options.Format, url),
		initialPath:    initialPath,
		options:        options,
		updateInterval: updateInterval,
		pauseManager:   service.FromContext[pause.Manager](ctx),
	}, nil
}

// ruleSetCacheKey addresses a cached rule-set by what it points at rather than by its tag. Two
// profiles that reference the same URL then share one cached payload, and two profiles that reuse
// one tag for different URLs never read each other's. Format participates because the same bytes
// are parsed differently under source and binary.
func ruleSetCacheKey(format string, url string) string {
	hash := sha256.Sum256([]byte(format + "\x00" + url))
	return hex.EncodeToString(hash[:])
}

func (s *RemoteRuleSet) Name() string {
	return s.tag
}

func (s *RemoteRuleSet) String() string {
	return strings.Join(F.MapToString(s.rules), " ")
}

func (s *RemoteRuleSet) StartContext(ctx context.Context, startContext *adapter.HTTPStartContext) error {
	s.cacheFile = service.FromContext[adapter.CacheFile](s.ctx)
	transport, err := s.resolveTransport()
	if err != nil {
		return E.Cause(err, "create rule-set http client")
	}
	startContext.Register(transport)
	s.httpClient = &http.Client{Transport: transport}
	if s.cacheFile != nil {
		if savedSet := s.cacheFile.LoadRuleSet(s.cacheKey); savedSet != nil {
			err = s.loadBytes(savedSet.Content)
			if err != nil {
				s.logger.Warn(E.Cause(err, "restore cached rule-set, will refetch"))
			} else {
				s.lastUpdated = savedSet.LastUpdated
				s.lastEtag = savedSet.LastEtag
				s.lastEtagURL = s.url
			}
		}
	}
	var loadedFromInitialPath bool
	if s.lastUpdated.IsZero() && s.initialPath != "" {
		var content []byte
		content, err = filemanager.ReadFile(s.ctx, s.initialPath)
		if err == nil {
			err = s.loadBytes(content)
		}
		if err != nil {
			s.logger.Warn(E.Cause(err, "load initial rule-set from ", s.initialPath))
		} else {
			loadedFromInitialPath = true
		}
	}
	if s.lastUpdated.IsZero() && !loadedFromInitialPath {
		err = s.fetch(ctx, true)
		if err != nil {
			return E.Cause(err, "initial rule-set: ", s.tag)
		}
	}
	return nil
}

func (s *RemoteRuleSet) Metadata() adapter.RuleSetMetadata {
	s.access.RLock()
	defer s.access.RUnlock()
	return s.metadata
}

func (s *RemoteRuleSet) ExtractIPSet() []*netipx.IPSet {
	s.access.RLock()
	defer s.access.RUnlock()
	return common.FlatMap(s.rules, extractIPSetFromRule)
}

func (s *RemoteRuleSet) IncRef() {
	s.refs.Add(1)
}

func (s *RemoteRuleSet) DecRef() {
	if s.refs.Add(-1) < 0 {
		panic("rule-set: negative refs")
	}
}

func (s *RemoteRuleSet) Cleanup() {
	if s.refs.Load() == 0 {
		s.rules = nil
	}
}

func (s *RemoteRuleSet) RegisterCallback(callback adapter.RuleSetUpdateCallback) *list.Element[adapter.RuleSetUpdateCallback] {
	s.access.Lock()
	defer s.access.Unlock()
	return s.callbacks.PushBack(callback)
}

func (s *RemoteRuleSet) UnregisterCallback(element *list.Element[adapter.RuleSetUpdateCallback]) {
	s.access.Lock()
	defer s.access.Unlock()
	s.callbacks.Remove(element)
}

func (s *RemoteRuleSet) loadBytes(content []byte) error {
	var (
		ruleSet option.PlainRuleSetCompat
		err     error
	)
	switch s.options.Format {
	case C.RuleSetFormatSource:
		ruleSet, err = json.UnmarshalExtended[option.PlainRuleSetCompat](content)
		if err != nil {
			return err
		}
	case C.RuleSetFormatBinary:
		ruleSet, err = srs.Read(bytes.NewReader(content), false)
		if err != nil {
			return err
		}
	default:
		return E.New("unknown rule-set format: ", s.options.Format)
	}
	plainRuleSet, err := ruleSet.Upgrade()
	if err != nil {
		return err
	}
	rules := make([]adapter.HeadlessRule, len(plainRuleSet.Rules))
	for i, ruleOptions := range plainRuleSet.Rules {
		rules[i], err = NewHeadlessRule(s.ctx, ruleOptions)
		if err != nil {
			return E.Cause(err, "parse rule_set.rules.[", i, "]")
		}
	}
	metadata := buildRuleSetMetadata(plainRuleSet.Rules)
	err = validateRuleSetMetadataUpdate(s.ctx, s.tag, metadata)
	if err != nil {
		return err
	}
	s.access.Lock()
	s.metadata = metadata
	s.rules = rules
	callbacks := s.callbacks.Array()
	s.access.Unlock()
	for _, callback := range callbacks {
		callback(s)
	}
	return nil
}

func (s *RemoteRuleSet) updateOnce() {
	err := s.fetch(s.ctx, false)
	if err != nil {
		s.logger.Error("fetch rule-set ", s.tag, ": ", err)
	} else if s.refs.Load() == 0 {
		s.rules = nil
	}
}

// nextUpdateDelay reports how long to wait before touching this rule-set again.
//
// A rule-set that has never loaded is empty, so every rule built on it is silently not matching and
// its traffic is falling through to route.final. Since initialization no longer fails on that, the
// only thing that repairs it is another fetch — waiting out the configured interval, a day by
// default, would leave routing degraded for that whole time over what is usually a transient
// network failure at boot. Deliberately a flat retry rather than a backoff: the cost of an attempt
// is one request that fails fast when the network is still down, and a backoff would only push the
// recovery back towards the interval it exists to avoid.
func (s *RemoteRuleSet) nextUpdateDelay() time.Duration {
	if s.lastUpdated.IsZero() {
		return min(ruleSetRetryInterval, s.updateInterval)
	}
	return s.updateInterval
}

// fetch tries the configured source first and, only if that fails, falls back: known mirrors of the
// same content over a direct connection, then the original URL over a direct connection. Trying the
// configured source first keeps the config's intent authoritative. The fallbacks exist because a
// brand-new profile has to download its rule-sets through a proxy that has not been validated yet,
// and one unreachable source used to abort the whole start.
//
// The error returned is always the configured source's, since that is the one the user can act on.
func (s *RemoteRuleSet) fetch(ctx context.Context, isStart bool) error {
	firstErr := s.fetchFrom(ctx, s.url, s.httpClient, isStart)
	if firstErr == nil {
		return nil
	}
	// A cancelled or expired caller context is not something another source can fix.
	if ctx.Err() != nil {
		return firstErr
	}
	// Only once the configured source has actually failed is a direct transport worth building.
	directClient, err := s.resolveDirectClient()
	if err != nil {
		s.logger.Debug("rule-set ", s.tag, " has no direct fallback: ", err)
		return firstErr
	}
	// The direct transport is not registered with the start context, so nothing else will ever drop
	// its keep-alive connections. Release them here instead of holding a descriptor per fallback
	// source for the lifetime of the process.
	defer directClient.CloseIdleConnections()
	// Bound the whole chain rather than each attempt: with fail-fast removed, every rule-set now
	// runs its fallbacks, and a per-attempt timeout alone would let one slow network multiply into
	// minutes of startup during which there is no working route.
	ctx, cancel := context.WithTimeout(ctx, ruleSetFallbackBudget)
	defer cancel()
	// ruleSetMirrorURLs returns a fresh slice, so appending the original URL cannot alias anything.
	for _, fallbackURL := range append(ruleSetMirrorURLs(s.url), s.url) {
		err = s.fetchFrom(ctx, fallbackURL, directClient, isStart)
		if err == nil {
			return nil
		}
		s.logger.Debug("rule-set ", s.tag, " fallback source ", fallbackURL, " failed: ", err)
		if ctx.Err() != nil {
			return firstErr
		}
	}
	return firstErr
}

// resolveDirectClient takes a reference to the manager's shared detour-free transport, on first use
// so that rule-sets whose configured source works never pay for one. On Android this dials through
// the platform interface's protected socket, so it genuinely leaves the tunnel.
//
// The transport is shared rather than built per rule-set: building one each would leave a tracked
// transport alive per failing rule-set until shutdown. Each rule-set still holds its own reference,
// so the CloseIdleConnections in fetch only takes effect once the last holder is done.
func (s *RemoteRuleSet) resolveDirectClient() (*http.Client, error) {
	s.directAccess.Lock()
	defer s.directAccess.Unlock()
	if s.directClient != nil {
		return s.directClient, nil
	}
	httpClientManager := service.FromContext[adapter.HTTPClientManager](s.ctx)
	if httpClientManager == nil {
		return nil, E.New("missing http client manager")
	}
	transport, err := httpClientManager.DirectTransport()
	if err != nil {
		return nil, err
	}
	s.directClient = &http.Client{Transport: transport}
	return s.directClient, nil
}

func (s *RemoteRuleSet) fetchFrom(ctx context.Context, sourceURL string, client *http.Client, isStart bool) error {
	s.logger.Debug("updating rule-set ", s.tag, " from URL: ", sourceURL)
	ctx, cancel := context.WithTimeout(ctx, ruleSetFetchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", sourceURL, nil)
	if err != nil {
		return err
	}
	// An ETag is only meaningful to the host that issued it, so never replay one across sources.
	if s.lastEtag != "" && s.lastEtagURL == sourceURL {
		request.Header.Set("If-None-Match", s.lastEtag)
	}
	if !isStart {
		defer client.CloseIdleConnections()
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		s.lastUpdated = time.Now()
		if s.cacheFile != nil {
			savedRuleSet := s.cacheFile.LoadRuleSet(s.cacheKey)
			if savedRuleSet != nil {
				savedRuleSet.LastUpdated = s.lastUpdated
				err = s.cacheFile.SaveRuleSet(s.cacheKey, savedRuleSet)
				if err != nil {
					s.logger.Error("save rule-set updated time: ", err)
					return nil
				}
			}
		}
		s.logger.Info("update rule-set ", s.tag, ": not modified")
		return nil
	default:
		return E.New("unexpected status: ", response.Status)
	}
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	err = s.loadBytes(content)
	if err != nil {
		return err
	}
	eTagHeader := response.Header.Get("Etag")
	if eTagHeader != "" {
		s.lastEtag = eTagHeader
		s.lastEtagURL = sourceURL
	}
	s.lastUpdated = time.Now()
	if s.cacheFile != nil {
		// Only the configured source's ETag is worth persisting: it is the only one a later run
		// will have a matching URL for.
		var savedEtag string
		if s.lastEtagURL == s.url {
			savedEtag = s.lastEtag
		}
		err = s.cacheFile.SaveRuleSet(s.cacheKey, &adapter.SavedBinary{
			LastUpdated: s.lastUpdated,
			Content:     content,
			LastEtag:    savedEtag,
		})
		if err != nil {
			s.logger.Error("save rule-set cache: ", err)
		}
	}
	s.logger.Info("updated rule-set ", s.tag)
	return nil
}

func (s *RemoteRuleSet) resolveTransport() (adapter.HTTPTransport, error) {
	httpClientManager := service.FromContext[adapter.HTTPClientManager](s.ctx)
	if s.options.RemoteOptions.HTTPClient != nil && !s.options.RemoteOptions.HTTPClient.IsEmpty() {
		if s.options.RemoteOptions.DownloadDetour != "" { //nolint:staticcheck
			return nil, E.New("http_client is conflict with deprecated download_detour field")
		}
		return httpClientManager.ResolveTransport(s.ctx, s.logger, *s.options.RemoteOptions.HTTPClient)
	}
	if s.options.RemoteOptions.DownloadDetour != "" { //nolint:staticcheck
		deprecated.Report(s.ctx, deprecated.OptionLegacyRuleSetDownloadDetour)
		return httpClientManager.ResolveTransport(s.ctx, s.logger, option.HTTPClientOptions{
			DialerOptions: option.DialerOptions{
				Detour: s.options.RemoteOptions.DownloadDetour, //nolint:staticcheck
			},
			DisableEmptyDirectCheck: true,
		})
	}
	defaultTransport := httpClientManager.DefaultTransport()
	if defaultTransport == nil {
		return nil, E.New("default http client transport is not initialized")
	}
	return defaultTransport, nil
}

func (s *RemoteRuleSet) Close() error {
	s.rules = nil
	s.cancel()
	return nil
}

func (s *RemoteRuleSet) Match(metadata *adapter.InboundContext) bool {
	return matchAnyHeadlessRule(s.rules, metadata)
}

func (s *RemoteRuleSet) mergeableRule() *DefaultHeadlessRule {
	return mergeableRuleIn(s.rules)
}
