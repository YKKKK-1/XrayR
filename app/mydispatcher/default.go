package mydispatcher

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	routingSession "github.com/xtls/xray-core/features/routing/session"
	"github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
	"golang.org/x/time/rate"

	"github.com/XrayR-project/XrayR/common/limiter"
	"github.com/XrayR-project/XrayR/common/rule"
)

var errSniffingTimeout = newError("timeout on sniffing")

type cachedReader struct {
	sync.Mutex
	reader   buf.TimeoutReader // *pipe.Reader or *buf.TimeoutWrapperReader
	cache    buf.MultiBuffer
	cacheErr error
}

func (r *cachedReader) Cache(b *buf.Buffer, deadline time.Duration) error {
	if r.cacheErr != nil {
		return r.cacheErr
	}
	mb, err := r.reader.ReadMultiBufferTimeout(deadline)
	if mb.IsEmpty() && err != nil {
		return err
	}
	r.Lock()
	if !mb.IsEmpty() {
		r.cacheErr = err
		r.cache, _ = buf.MergeMulti(r.cache, mb)
	}
	b.Clear()
	rawBytes := b.Extend(min(r.cache.Len(), b.Cap()))
	n := r.cache.Copy(rawBytes)
	b.Resize(0, int32(n))
	r.Unlock()
	return nil
}

func (r *cachedReader) readInternal() (buf.MultiBuffer, error) {
	r.Lock()
	defer r.Unlock()

	if r.cache != nil && !r.cache.IsEmpty() {
		mb := r.cache
		err := r.cacheErr
		r.cache, r.cacheErr = nil, nil
		return mb, err
	}

	return nil, nil
}

func (r *cachedReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.readInternal()
	if mb != nil {
		return mb, err
	}

	return r.reader.ReadMultiBuffer()
}

func (r *cachedReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	mb, err := r.readInternal()
	if mb != nil {
		return mb, err
	}

	return r.reader.ReadMultiBufferTimeout(timeout)
}

func (r *cachedReader) Interrupt() {
	r.Lock()
	if r.cache != nil {
		r.cache = buf.ReleaseMulti(r.cache)
	}
	r.Unlock()
	if p, ok := r.reader.(*pipe.Reader); ok {
		p.Interrupt()
	}
}

// DefaultDispatcher is a default implementation of Dispatcher.
type DefaultDispatcher struct {
	ohm         outbound.Manager
	router      routing.Router
	policy      policy.Manager
	stats       stats.Manager
	dns         dns.Client
	fdns        dns.FakeDNSEngine
	Limiter     *limiter.Limiter
	RuleManager *rule.Manager
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		d := new(DefaultDispatcher)
		if err := core.RequireFeatures(ctx, func(om outbound.Manager, router routing.Router, pm policy.Manager, sm stats.Manager, dc dns.Client) error {
			core.OptionalFeatures(ctx, func(fdns dns.FakeDNSEngine) {
				d.fdns = fdns
			})
			return d.Init(config.(*Config), om, router, pm, sm, dc)
		}); err != nil {
			return nil, err
		}
		return d, nil
	}))
}

// Init initializes DefaultDispatcher.
func (d *DefaultDispatcher) Init(config *Config, om outbound.Manager, router routing.Router, pm policy.Manager, sm stats.Manager, dns dns.Client) error {
	d.ohm = om
	d.router = router
	d.policy = pm
	d.stats = sm
	d.Limiter = limiter.New()
	d.RuleManager = rule.New()
	d.dns = dns
	return nil
}

// Type implements common.HasType.
func (*DefaultDispatcher) Type() interface{} {
	return routing.DispatcherType()
}

// Start implements common.Runnable.
func (*DefaultDispatcher) Start() error {
	return nil
}

// Close implements common.Closable.
func (*DefaultDispatcher) Close() error {
	return nil
}

func (d *DefaultDispatcher) userBucket(ctx context.Context) (*rate.Limiter, error) {
	in := session.InboundFromContext(ctx)
	if in == nil || in.User == nil || in.User.Email == "" || in.Source.Address == nil {
		return nil, nil
	}
	bucket, limited, reject := d.Limiter.GetUserBucket(in.Tag, in.User.Email, in.Source.Address.String())
	if reject {
		return nil, newError("Devices reach the limit: ", in.User.Email)
	}
	if limited {
		// Splice bypasses the writers that enforce a user's speed limit.
		in.CanSpliceCopy = 3
		return bucket, nil
	}
	return nil, nil
}

func (d *DefaultDispatcher) getLink(ctx context.Context) (*transport.Link, *transport.Link, error) {
	opt := pipe.OptionsFromContext(ctx)
	uplinkReader, uplinkWriter := pipe.New(opt...)
	downlinkReader, downlinkWriter := pipe.New(opt...)

	inboundLink := &transport.Link{
		Reader: downlinkReader,
		Writer: uplinkWriter,
	}

	outboundLink := &transport.Link{
		Reader: uplinkReader,
		Writer: downlinkWriter,
	}

	sessionInbound := session.InboundFromContext(ctx)
	var user *protocol.MemoryUser
	if sessionInbound != nil {
		user = sessionInbound.User
	}

	if user != nil && len(user.Email) > 0 {
		// Speed Limit and Device Limit
		bucket, err := d.userBucket(ctx)
		if err != nil {
			common.Close(outboundLink.Writer)
			common.Close(inboundLink.Writer)
			common.Interrupt(outboundLink.Reader)
			common.Interrupt(inboundLink.Reader)
			return nil, nil, err
		}
		if bucket != nil {
			inboundLink.Writer = d.Limiter.RateWriterContext(ctx, inboundLink.Writer, bucket)
			outboundLink.Writer = d.Limiter.RateWriterContext(ctx, outboundLink.Writer, bucket)
		}

		p := d.policy.ForLevel(user.Level)
		if p.Stats.UserUplink {
			name := "user>>>" + user.Email + ">>>traffic>>>uplink"
			if c, _ := d.stats.GetOrRegisterCounter(name); c != nil {
				inboundLink.Writer = &SizeStatWriter{
					Counter: c,
					Writer:  inboundLink.Writer,
				}
			}
		}
		if p.Stats.UserDownlink {
			name := "user>>>" + user.Email + ">>>traffic>>>downlink"
			if c, _ := d.stats.GetOrRegisterCounter(name); c != nil {
				outboundLink.Writer = &SizeStatWriter{
					Counter: c,
					Writer:  outboundLink.Writer,
				}
			}
		}
	}

	return inboundLink, outboundLink, nil
}

func (d *DefaultDispatcher) shouldOverride(ctx context.Context, result SniffResult, request session.SniffingRequest, destination net.Destination) bool {
	domain := result.Domain()
	if domain == "" {
		return false
	}
	if request.ExcludeForDomain != nil && request.ExcludeForDomain.MatchAny(strings.ToLower(domain)) {
		return false
	}
	if request.ExcludeForIP != nil && destination.Address.Family().IsIP() && request.ExcludeForIP.Match(destination.Address.IP()) {
		return false
	}
	protocolString := result.Protocol()
	if resComp, ok := result.(SnifferResultComposite); ok {
		protocolString = resComp.ProtocolForDomainResult()
	}
	for _, p := range request.OverrideDestinationForProtocol {
		if strings.HasPrefix(protocolString, p) || strings.HasPrefix(p, protocolString) {
			return true
		}
		if fkr0, ok := d.fdns.(dns.FakeDNSEngineRev0); ok && protocolString != "bittorrent" && p == "fakedns" &&
			fkr0.IsIPInIPPool(destination.Address) {
			errors.LogInfo(ctx, "Using sniffer ", protocolString, " since the fake DNS missed")
			return true
		}
		if resultSubset, ok := result.(SnifferIsProtoSubsetOf); ok {
			if resultSubset.IsProtoSubsetOf(p) {
				return true
			}
		}
	}

	return false
}

// Dispatch implements routing.Dispatcher.
func (d *DefaultDispatcher) Dispatch(ctx context.Context, destination net.Destination) (*transport.Link, error) {
	if !destination.IsValid() {
		return nil, newError("Dispatcher: Invalid destination.")
	}
	inbound, outbound, err := d.getLink(ctx)
	if err != nil {
		return nil, err
	}
	ctx = prepareDispatch(ctx, destination)
	go d.dispatch(ctx, destination, outbound)
	return inbound, nil
}

// DispatchLink owns the supplied link until the outbound finishes.
func (d *DefaultDispatcher) DispatchLink(ctx context.Context, destination net.Destination, link *transport.Link) error {
	if !destination.IsValid() {
		return newError("Dispatcher: Invalid destination.")
	}
	bucket, err := d.userBucket(ctx)
	if err != nil {
		return err
	}
	if bucket != nil {
		link.Reader = d.Limiter.RateReader(ctx, link.Reader, bucket)
		link.Writer = d.Limiter.RateWriterContext(ctx, link.Writer, bucket)
	}
	if runtime.GOOS == "windows" && bucket == nil && destination.Network == net.Network_TCP {
		in := session.InboundFromContext(ctx)
		if _, vision := link.Reader.(*proxy.VisionReader); vision && in != nil && in.User != nil {
			raw, _, _ := proxy.UnwrapRawConn(in.Conn)
			limit := d.policy.ForLevel(in.User.Level).Buffer.PerConnection
			if tcp, ok := raw.(*net.TCPConn); ok && limit > 0 {
				finish := bufferVisionUpload(link, tcp, limit)
				defer finish()
			}
		}
	}
	ctx = prepareDispatch(ctx, destination)
	d.dispatch(ctx, destination, dispatcher.WrapLink(ctx, d.policy, d.stats, link))
	return nil
}

// Windows has no splice path. Reading the Vision stream concurrently lets the
// outbound batch uploads while the next socket read is in flight.
func bufferVisionUpload(link *transport.Link, conn *net.TCPConn, limit int32) func() {
	reader, writer := pipe.New(pipe.WithSizeLimit(limit))
	source := link.Reader
	link.Reader = reader
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := buf.Copy(source, writer); err != nil {
			writer.Interrupt()
		} else {
			writer.Close()
		}
	}()
	return func() {
		reader.Interrupt()
		select {
		case <-done:
		default:
			// The outbound may finish before the peer closes its upload.
			conn.SetReadDeadline(time.Now())
			<-done
		}
	}
}

func prepareDispatch(ctx context.Context, destination net.Destination) context.Context {
	outbounds := session.OutboundsFromContext(ctx)
	if len(outbounds) == 0 {
		outbounds = []*session.Outbound{{}}
		ctx = session.ContextWithOutbounds(ctx, outbounds)
	}
	ob := outbounds[len(outbounds)-1]
	ob.OriginalTarget = destination
	ob.Target = destination
	content := session.ContentFromContext(ctx)
	if content == nil {
		content = new(session.Content)
		ctx = session.ContextWithContent(ctx, content)
	}

	return ctx
}

func (d *DefaultDispatcher) dispatch(ctx context.Context, destination net.Destination, outbound *transport.Link) {
	outbounds := session.OutboundsFromContext(ctx)
	ob := outbounds[len(outbounds)-1]
	content := session.ContentFromContext(ctx)

	sniffingRequest := content.SniffingRequest
	if sniffingRequest.Enabled {
		cReader := &cachedReader{
			reader: outbound.Reader.(buf.TimeoutReader),
		}
		outbound.Reader = cReader
		result, err := sniffer(ctx, cReader, sniffingRequest.MetadataOnly, destination.Network)
		if err == nil {
			content.Protocol = result.Protocol()
		}
		if err == nil && d.shouldOverride(ctx, result, sniffingRequest, destination) {
			domain := result.Domain()
			errors.LogInfo(ctx, "sniffed domain: ", domain)
			destination.Address = net.ParseAddress(domain)
			protocol := result.Protocol()
			if resComp, ok := result.(SnifferResultComposite); ok {
				protocol = resComp.ProtocolForDomainResult()
			}
			isFakeIP := false
			if fkr0, ok := d.fdns.(dns.FakeDNSEngineRev0); ok && fkr0.IsIPInIPPool(ob.Target.Address) {
				isFakeIP = true
			}
			if sniffingRequest.RouteOnly && protocol != "fakedns" && protocol != "fakedns+others" && !isFakeIP {
				ob.RouteTarget = destination
			} else {
				ob.Target = destination
			}
		}
	}
	d.routedDispatch(ctx, outbound, destination)
}

func sniffer(ctx context.Context, cReader *cachedReader, metadataOnly bool, network net.Network) (SniffResult, error) {
	payload := buf.NewWithSize(32767)
	defer payload.Release()

	sniffer := NewSniffer(ctx)

	metaresult, metadataErr := sniffer.SniffMetadata(ctx)

	if metadataOnly {
		return metaresult, metadataErr
	}

	contentResult, contentErr := func() (SniffResult, error) {
		cacheDeadline := 200 * time.Millisecond
		totalAttempt := 0
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				cachingStartingTimeStamp := time.Now()
				err := cReader.Cache(payload, cacheDeadline)
				if err != nil {
					return nil, err
				}
				cachingTimeElapsed := time.Since(cachingStartingTimeStamp)
				cacheDeadline -= cachingTimeElapsed

				if !payload.IsEmpty() {
					result, err := sniffer.Sniff(ctx, payload.Bytes(), network)
					switch err {
					case common.ErrNoClue: // No Clue: protocol not matches, and sniffer cannot determine whether there will be a match or not
						totalAttempt++
					case protocol.ErrProtoNeedMoreData: // Protocol Need More Data: protocol matches, but need more data to complete sniffing
						// in this case, do not add totalAttempt(allow to read until timeout)
					default:
						return result, err
					}
				} else {
					totalAttempt++
				}
				if totalAttempt >= 2 || cacheDeadline <= 0 {
					return nil, errSniffingTimeout
				}
			}
		}
	}()
	if contentErr != nil && metadataErr == nil {
		return metaresult, nil
	}
	if contentErr == nil && metadataErr == nil {
		return CompositeResult(metaresult, contentResult), nil
	}
	return contentResult, contentErr
}

func (d *DefaultDispatcher) routedDispatch(ctx context.Context, link *transport.Link, destination net.Destination) {
	// DNS hosts lookup functionality is no longer available in the current Xray core version
	// The hosts lookup feature has been deprecated/moved in the new DNS implementation

	var handler outbound.Handler

	// Check if domain and protocol hit the rule
	sessionInbound := session.InboundFromContext(ctx)
	// Whether the inbound connection contains a user
	if sessionInbound != nil && sessionInbound.User != nil {
		if d.RuleManager.Detect(sessionInbound.Tag, destination.String(), sessionInbound.User.Email) {
			errors.LogError(ctx, fmt.Sprintf("User %s access %s reject by rule", sessionInbound.User.Email, destination.String()))
			newError("destination is reject by rule")
			common.Close(link.Writer)
			common.Interrupt(link.Reader)
			return
		}
	}

	routingLink := routingSession.AsRoutingContext(ctx)
	inTag := routingLink.GetInboundTag()
	isPickRoute := 0
	if forcedOutboundTag := session.GetForcedOutboundTagFromContext(ctx); forcedOutboundTag != "" {
		ctx = session.SetForcedOutboundTagToContext(ctx, "")
		if h := d.ohm.GetHandler(forcedOutboundTag); h != nil {
			isPickRoute = 1
			errors.LogInfo(ctx, "taking platform initialized detour [", forcedOutboundTag, "] for [", destination, "]")
			handler = h
		} else {
			errors.LogError(ctx, "non existing tag for platform initialized detour: ", forcedOutboundTag)
			common.Close(link.Writer)
			common.Interrupt(link.Reader)
			return
		}
	} else if d.router != nil {
		if route, err := d.router.PickRoute(routingLink); err == nil {
			outTag := route.GetOutboundTag()
			if h := d.ohm.GetHandler(outTag); h != nil {
				isPickRoute = 2
				errors.LogInfo(ctx, "taking detour [", outTag, "] for [", destination, "]")
				handler = h
			} else {
				errors.LogWarning(ctx, "non existing outTag: ", outTag)
			}
		} else {
			errors.LogInfo(ctx, "default route for ", destination)
		}
	}

	if handler == nil {
		handler = d.ohm.GetHandler(inTag) // Default outbound handler tag should be as same as the inbound tag
	}

	// If there is no outbound with tag as same as the inbound tag
	if handler == nil {
		handler = d.ohm.GetDefaultHandler()
	}

	if handler == nil {
		errors.LogInfo(ctx, "default outbound handler not exist")
		common.Close(link.Writer)
		common.Interrupt(link.Reader)
		return
	}

	outbounds := session.OutboundsFromContext(ctx)
	outbounds[len(outbounds)-1].Tag = handler.Tag()
	if accessMessage := log.AccessMessageFromContext(ctx); accessMessage != nil {
		if tag := handler.Tag(); tag != "" {
			if inTag == "" {
				accessMessage.Detour = tag
			} else if isPickRoute == 1 {
				accessMessage.Detour = inTag + " ==> " + tag
			} else if isPickRoute == 2 {
				accessMessage.Detour = inTag + " -> " + tag
			} else {
				accessMessage.Detour = inTag + " >> " + tag
			}
		}
		log.Record(accessMessage)
	}

	handler.Dispatch(ctx, link)
}
