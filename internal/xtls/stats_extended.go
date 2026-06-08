package xtls

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
)

const getUsersStatsMethod = "/xray.app.stats.command.StatsService/GetUsersStats"

func init() {
	encoding.RegisterCodec(bytesCodec{})
}

type bytesCodec struct{}

func (bytesCodec) Name() string { return "proto-bytes" }

func (bytesCodec) Marshal(v any) ([]byte, error) {
	msg, ok := v.(*bytesMessage)
	if !ok || msg == nil {
		return nil, fmt.Errorf("unexpected message type %T", v)
	}
	return msg.data, nil
}

func (bytesCodec) Unmarshal(data []byte, v any) error {
	msg, ok := v.(*bytesMessage)
	if !ok || msg == nil {
		return fmt.Errorf("unexpected message type %T", v)
	}
	msg.data = append(msg.data[:0], data...)
	return nil
}

type bytesMessage struct {
	data []byte
}

func (s *StatsAPI) getAllUsersStatsExtended(ctx context.Context, reset bool) ([]UserTraffic, error) {
	if s.conn == nil {
		return nil, status.Error(codes.Unimplemented, "grpc connection unavailable")
	}

	req := &bytesMessage{data: encodeGetUsersStatsRequest(true, reset)}
	resp := &bytesMessage{}
	if err := s.conn.Invoke(
		ctx,
		getUsersStatsMethod,
		req,
		resp,
		grpc.ForceCodec(bytesCodec{}),
	); err != nil {
		return nil, err
	}
	return decodeGetUsersStatsResponse(resp.data)
}

func encodeGetUsersStatsRequest(includeTraffic, reset bool) []byte {
	var out []byte
	if includeTraffic {
		out = protowire.AppendTag(out, 1, protowire.VarintType)
		out = protowire.AppendVarint(out, 1)
	}
	if reset {
		out = protowire.AppendTag(out, 2, protowire.VarintType)
		out = protowire.AppendVarint(out, 1)
	}
	return out
}

func decodeGetUsersStatsResponse(raw []byte) ([]UserTraffic, error) {
	users := make([]UserTraffic, 0)
	for len(raw) > 0 {
		num, wireType, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		raw = raw[n:]
		if num != 1 || wireType != protowire.BytesType {
			n = protowire.ConsumeFieldValue(num, wireType, raw)
			if n < 0 {
				return nil, protowire.ParseError(n)
			}
			raw = raw[n:]
			continue
		}
		value, n := protowire.ConsumeBytes(raw)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		raw = raw[n:]
		user, err := decodeUserStat(value)
		if err != nil {
			return nil, err
		}
		if user.Username != "" {
			users = append(users, user)
		}
	}
	return users, nil
}

func decodeUserStat(raw []byte) (UserTraffic, error) {
	var user UserTraffic
	for len(raw) > 0 {
		num, wireType, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return user, protowire.ParseError(n)
		}
		raw = raw[n:]
		switch num {
		case 1:
			if wireType != protowire.BytesType {
				continue
			}
			value, consumed := protowire.ConsumeBytes(raw)
			if consumed < 0 {
				return user, protowire.ParseError(consumed)
			}
			raw = raw[consumed:]
			user.Username = string(value)
		case 3:
			if wireType != protowire.BytesType {
				continue
			}
			value, consumed := protowire.ConsumeBytes(raw)
			if consumed < 0 {
				return user, protowire.ParseError(consumed)
			}
			raw = raw[consumed:]
			uplink, downlink, err := decodeTrafficUserStat(value)
			if err != nil {
				return user, err
			}
			user.Uplink = uplink
			user.Downlink = downlink
		default:
			n = protowire.ConsumeFieldValue(num, wireType, raw)
			if n < 0 {
				return user, protowire.ParseError(n)
			}
			raw = raw[n:]
		}
	}
	return user, nil
}

func decodeTrafficUserStat(raw []byte) (uplink, downlink int64, err error) {
	for len(raw) > 0 {
		num, wireType, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return 0, 0, protowire.ParseError(n)
		}
		raw = raw[n:]
		if wireType != protowire.VarintType {
			n = protowire.ConsumeFieldValue(num, wireType, raw)
			if n < 0 {
				return 0, 0, protowire.ParseError(n)
			}
			raw = raw[n:]
			continue
		}
		value, consumed := protowire.ConsumeVarint(raw)
		if consumed < 0 {
			return 0, 0, protowire.ParseError(consumed)
		}
		raw = raw[consumed:]
		switch num {
		case 1:
			uplink = int64(value)
		case 2:
			downlink = int64(value)
		}
	}
	return uplink, downlink, nil
}

func isGRPCUnimplemented(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.Unimplemented
}
