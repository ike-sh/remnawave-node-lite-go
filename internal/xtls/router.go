package xtls

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net"

	routerproto "github.com/xtls/xray-core/app/router"
	routercommand "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/grpc"
)

const visionBlockOutboundTag = "BLOCK"

type RouterAPI struct {
	client routercommand.RoutingServiceClient
}

func NewRouterAPI(conn *grpc.ClientConn) *RouterAPI {
	return &RouterAPI{client: routercommand.NewRoutingServiceClient(conn)}
}

func (r *RouterAPI) AddSrcIPRule(ctx context.Context, ip, ruleTag string, appendRule bool) error {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("invalid ip: %s", ip)
	}

	prefix := uint32(128)
	ipBytes := parsed.To16()
	if ip4 := parsed.To4(); ip4 != nil {
		prefix = 32
		ipBytes = ip4
	}

	rule := &routerproto.RoutingRule{
		RuleTag: ruleTag,
		SourceGeoip: []*routerproto.GeoIP{{
			Cidr: []*routerproto.CIDR{{
				Ip:     ipBytes,
				Prefix: prefix,
			}},
		}},
		TargetTag: &routerproto.RoutingRule_Tag{Tag: visionBlockOutboundTag},
	}

	_, err := r.client.AddRule(ctx, &routercommand.AddRuleRequest{
		Config:       serial.ToTypedMessage(rule),
		ShouldAppend: appendRule,
	})
	return err
}

func (r *RouterAPI) RemoveRuleByTag(ctx context.Context, ruleTag string) error {
	_, err := r.client.RemoveRule(ctx, &routercommand.RemoveRuleRequest{RuleTag: ruleTag})
	return err
}

func HashIPRuleTag(ip string) string {
	sum := md5.Sum([]byte(ip))
	return hex.EncodeToString(sum[:])
}
