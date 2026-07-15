package contract

func requestSchemas() map[string]*Schema {
	return map[string]*Schema{
		"xray.start":                     startXrayRequestSchema(),
		"stats.user-online-status":       object(map[string]*Schema{"username": stringValue()}, "username"),
		"stats.users":                    resetRequestSchema(),
		"stats.inbound":                  tagResetRequestSchema(),
		"stats.outbound":                 tagResetRequestSchema(),
		"stats.all-inbounds":             resetRequestSchema(),
		"stats.all-outbounds":            resetRequestSchema(),
		"stats.combined":                 resetRequestSchema(),
		"stats.user-ip-list":             object(map[string]*Schema{"userId": stringValue()}, "userId"),
		"handler.add-user":               addUserRequestSchema(),
		"handler.remove-user":            removeUserRequestSchema(),
		"handler.inbound-users-count":    tagRequestSchema(),
		"handler.inbound-users":          tagRequestSchema(),
		"handler.add-users":              addUsersRequestSchema(),
		"handler.remove-users":           removeUsersRequestSchema(),
		"handler.drop-users-connections": object(map[string]*Schema{"userIds": array(stringValue(), 1)}, "userIds"),
		"handler.drop-ips":               object(map[string]*Schema{"ips": array(stringValue(), 1)}, "ips"),
		"plugin.sync":                    syncPluginRequestSchema(),
		"plugin.nftables.block-ips":      blockIPsRequestSchema(),
		"plugin.nftables.unblock-ips":    object(map[string]*Schema{"ips": array(stringFormat("ip"))}, "ips"),
	}
}

func responseSchemas() map[string]*Schema {
	genericHandler := responseEnvelope(object(map[string]*Schema{
		"success": booleanValue(),
		"error":   nullable(stringValue()),
	}, "success", "error"))
	successOnly := responseEnvelope(object(map[string]*Schema{"success": booleanValue()}, "success"))
	accepted := responseEnvelope(object(map[string]*Schema{"accepted": booleanValue()}, "accepted"))
	tagTraffic := func(name string) *Schema {
		return object(map[string]*Schema{
			name:       stringValue(),
			"downlink": numberValue(),
			"uplink":   numberValue(),
		}, name, "downlink", "uplink")
	}

	return map[string]*Schema{
		"xray.start": responseEnvelope(object(map[string]*Schema{
			"isStarted":       booleanValue(),
			"version":         nullable(stringValue()),
			"error":           nullable(stringValue()),
			"nodeInformation": object(map[string]*Schema{"version": nullable(stringValue())}, "version"),
			"system":          nodeSystemSchema(),
		}, "isStarted", "version", "error", "nodeInformation", "system")),
		"xray.stop": responseEnvelope(object(map[string]*Schema{
			"isStopped": booleanValue(),
		}, "isStopped")),
		"xray.healthcheck": responseEnvelope(object(map[string]*Schema{
			"isAlive":                  booleanValue(),
			"xrayInternalStatusCached": booleanValue(),
			"xrayVersion":              nullable(stringValue()),
			"nodeVersion":              stringValue(),
		}, "isAlive", "xrayInternalStatusCached", "xrayVersion", "nodeVersion")),
		"stats.user-online-status": responseEnvelope(object(map[string]*Schema{
			"isOnline": booleanValue(),
		}, "isOnline")),
		"stats.users": responseEnvelope(object(map[string]*Schema{
			"users": array(object(map[string]*Schema{
				"username": stringValue(),
				"downlink": numberValue(),
				"uplink":   numberValue(),
			}, "username", "downlink", "uplink")),
		}, "users")),
		"stats.system":   systemStatsResponseSchema(),
		"stats.inbound":  responseEnvelope(tagTraffic("inbound")),
		"stats.outbound": responseEnvelope(tagTraffic("outbound")),
		"stats.all-outbounds": responseEnvelope(object(map[string]*Schema{
			"outbounds": array(tagTraffic("outbound")),
		}, "outbounds")),
		"stats.all-inbounds": responseEnvelope(object(map[string]*Schema{
			"inbounds": array(tagTraffic("inbound")),
		}, "inbounds")),
		"stats.combined": responseEnvelope(object(map[string]*Schema{
			"inbounds":  array(tagTraffic("inbound")),
			"outbounds": array(tagTraffic("outbound")),
		}, "inbounds", "outbounds")),
		"stats.user-ip-list": responseEnvelope(object(map[string]*Schema{
			"ips": array(ipEntrySchema()),
		}, "ips")),
		"stats.users-ip-list": responseEnvelope(object(map[string]*Schema{
			"users": array(object(map[string]*Schema{
				"userId": stringValue(),
				"ips":    array(ipEntrySchema()),
			}, "userId", "ips")),
		}, "users")),
		"handler.add-user":            genericHandler,
		"handler.remove-user":         genericHandler,
		"handler.inbound-users-count": responseEnvelope(object(map[string]*Schema{"count": numberValue()}, "count")),
		"handler.inbound-users": responseEnvelope(object(map[string]*Schema{
			"users": array(object(map[string]*Schema{
				"username": stringValue(),
				"email":    stringValue(),
				"level":    numberValue(),
			}, "username")),
		}, "users")),
		"handler.add-users":               genericHandler,
		"handler.remove-users":            genericHandler,
		"handler.drop-users-connections":  successOnly,
		"handler.drop-ips":                successOnly,
		"plugin.sync":                     accepted,
		"plugin.torrent-blocker.collect":  collectReportsResponseSchema(),
		"plugin.nftables.block-ips":       accepted,
		"plugin.nftables.unblock-ips":     accepted,
		"plugin.nftables.recreate-tables": accepted,
	}
}

func responseEnvelope(response *Schema) *Schema {
	return object(map[string]*Schema{"response": response}, "response")
}

func resetRequestSchema() *Schema {
	return object(map[string]*Schema{"reset": booleanValue()}, "reset")
}

func tagRequestSchema() *Schema {
	return object(map[string]*Schema{"tag": stringValue()}, "tag")
}

func tagResetRequestSchema() *Schema {
	return object(map[string]*Schema{
		"tag":   stringValue(),
		"reset": booleanValue(),
	}, "tag", "reset")
}

func startXrayRequestSchema() *Schema {
	return object(map[string]*Schema{
		"internals": object(map[string]*Schema{
			"forceRestart": booleanValue(),
			"hashes": object(map[string]*Schema{
				"emptyConfig": stringValue(),
				"inbounds": array(object(map[string]*Schema{
					"usersCount": numberValue(),
					"hash":       stringValue(),
					"tag":        stringValue(),
				}, "usersCount", "hash", "tag")),
			}, "emptyConfig", "inbounds"),
		}, "hashes"),
		"xrayConfig": record(anyValue()),
	}, "internals", "xrayConfig")
}

func addUserRequestSchema() *Schema {
	base := func(userType string, fields map[string]*Schema, required ...string) *Schema {
		fields["type"] = stringEnum(userType)
		return object(fields, append([]string{"type", "tag", "username"}, required...)...)
	}
	users := oneOf(
		base("trojan", map[string]*Schema{
			"tag": stringValue(), "username": stringValue(), "password": stringValue(),
		}, "password"),
		base("vless", map[string]*Schema{
			"tag": stringValue(), "username": stringValue(), "uuid": stringValue(),
			"flow": stringEnum("xtls-rprx-vision", ""),
		}, "uuid", "flow"),
		base("shadowsocks", map[string]*Schema{
			"tag": stringValue(), "username": stringValue(), "password": stringValue(),
			"cipherType": numberEnum(-1, 0, 5, 6, 7, 8, 9), "ivCheck": booleanValue(),
		}, "password", "cipherType", "ivCheck"),
		base("shadowsocks22", map[string]*Schema{
			"tag": stringValue(), "username": stringValue(), "password": stringValue(),
		}, "password"),
		base("hysteria", map[string]*Schema{
			"tag": stringValue(), "username": stringValue(), "password": stringValue(),
		}, "password"),
	)
	return object(map[string]*Schema{
		"data": array(users),
		"hashData": object(map[string]*Schema{
			"vlessUuid":     stringFormat("uuid"),
			"prevVlessUuid": stringFormat("uuid"),
		}, "vlessUuid"),
	}, "data", "hashData")
}

func removeUserRequestSchema() *Schema {
	return object(map[string]*Schema{
		"username": stringValue(),
		"hashData": object(map[string]*Schema{
			"vlessUuid": stringFormat("uuid"),
		}, "vlessUuid"),
	}, "username", "hashData")
}

func addUsersRequestSchema() *Schema {
	baseInbound := func(userType string, fields map[string]*Schema, required ...string) *Schema {
		fields["type"] = stringEnum(userType)
		return object(fields, append([]string{"type", "tag"}, required...)...)
	}
	inbound := oneOf(
		baseInbound("trojan", map[string]*Schema{"tag": stringValue()}),
		baseInbound("vless", map[string]*Schema{
			"tag": stringValue(), "flow": stringEnum("xtls-rprx-vision", ""),
		}, "flow"),
		baseInbound("shadowsocks", map[string]*Schema{"tag": stringValue()}),
		baseInbound("shadowsocks22", map[string]*Schema{"tag": stringValue()}),
		baseInbound("hysteria", map[string]*Schema{"tag": stringValue()}),
	)
	user := object(map[string]*Schema{
		"inboundData": array(inbound),
		"userData": object(map[string]*Schema{
			"userId":         stringValue(),
			"hashUuid":       stringFormat("uuid"),
			"vlessUuid":      stringFormat("uuid"),
			"trojanPassword": stringValue(),
			"ssPassword":     stringValue(),
		}, "userId", "hashUuid", "vlessUuid", "trojanPassword", "ssPassword"),
	}, "inboundData", "userData")
	return object(map[string]*Schema{
		"affectedInboundTags": array(stringValue()),
		"users":               array(user),
	}, "affectedInboundTags", "users")
}

func removeUsersRequestSchema() *Schema {
	return object(map[string]*Schema{
		"users": array(object(map[string]*Schema{
			"userId":   stringValue(),
			"hashUuid": stringFormat("uuid"),
		}, "userId", "hashUuid")),
	}, "users")
}

func syncPluginRequestSchema() *Schema {
	plugin := object(map[string]*Schema{
		"config": record(anyValue()),
		"uuid":   stringFormat("uuid"),
		"name":   stringValue(),
	}, "config", "uuid", "name")
	return object(map[string]*Schema{"plugin": nullable(plugin)}, "plugin")
}

func blockIPsRequestSchema() *Schema {
	return object(map[string]*Schema{
		"ips": array(object(map[string]*Schema{
			"ip":      stringFormat("ip"),
			"timeout": numberValue(),
		}, "ip", "timeout")),
	}, "ips")
}

func networkInterfaceSchema() *Schema {
	return object(map[string]*Schema{
		"interface":     stringValue(),
		"rxBytesPerSec": numberValue(),
		"txBytesPerSec": numberValue(),
		"rxTotal":       numberValue(),
		"txTotal":       numberValue(),
	}, "interface", "rxBytesPerSec", "txBytesPerSec", "rxTotal", "txTotal")
}

func nodeSystemInfoSchema() *Schema {
	return object(map[string]*Schema{
		"arch":              stringValue(),
		"cpus":              integerValue(),
		"cpuModel":          stringValue(),
		"memoryTotal":       numberValue(),
		"hostname":          stringValue(),
		"platform":          stringValue(),
		"release":           stringValue(),
		"type":              stringValue(),
		"version":           stringValue(),
		"networkInterfaces": array(stringValue()),
	}, "arch", "cpus", "cpuModel", "memoryTotal", "hostname", "platform", "release", "type", "version", "networkInterfaces")
}

func nodeSystemStatsSchema() *Schema {
	return object(map[string]*Schema{
		"memoryFree": numberValue(),
		"memoryUsed": numberValue(),
		"uptime":     numberValue(),
		"loadAvg":    array(numberValue()),
		"interface":  nullable(networkInterfaceSchema()),
	}, "memoryFree", "memoryUsed", "uptime", "loadAvg", "interface")
}

func nodeSystemSchema() *Schema {
	return object(map[string]*Schema{
		"info":  nodeSystemInfoSchema(),
		"stats": nodeSystemStatsSchema(),
	}, "info", "stats")
}

func sysStatsSchema() *Schema {
	return object(map[string]*Schema{
		"numGoroutine": numberValue(),
		"numGC":        numberValue(),
		"alloc":        numberValue(),
		"totalAlloc":   numberValue(),
		"sys":          numberValue(),
		"mallocs":      numberValue(),
		"frees":        numberValue(),
		"liveObjects":  numberValue(),
		"pauseTotalNs": numberValue(),
		"uptime":       numberValue(),
	}, "numGoroutine", "numGC", "alloc", "totalAlloc", "sys", "mallocs", "frees", "liveObjects", "pauseTotalNs", "uptime")
}

func systemStatsResponseSchema() *Schema {
	return responseEnvelope(object(map[string]*Schema{
		"xrayInfo": nullable(sysStatsSchema()),
		"plugins": object(map[string]*Schema{
			"torrentBlocker": object(map[string]*Schema{
				"reportsCount": numberValue(),
			}, "reportsCount"),
		}, "torrentBlocker"),
		"system": object(map[string]*Schema{
			"stats": nodeSystemStatsSchema(),
		}, "stats"),
	}, "xrayInfo", "plugins", "system"))
}

func ipEntrySchema() *Schema {
	return object(map[string]*Schema{
		"ip":       stringValue(),
		"lastSeen": stringFormat("date-time"),
	}, "ip", "lastSeen")
}

func collectReportsResponseSchema() *Schema {
	webhook := object(map[string]*Schema{
		"email":          nullable(stringValue()),
		"level":          nullable(numberValue()),
		"protocol":       nullable(stringValue()),
		"network":        stringValue(),
		"source":         nullable(stringValue()),
		"destination":    stringValue(),
		"routeTarget":    nullable(stringValue()),
		"originalTarget": nullable(stringValue()),
		"inboundTag":     nullable(stringValue()),
		"inboundName":    nullable(stringValue()),
		"inboundLocal":   nullable(stringValue()),
		"outboundTag":    nullable(stringValue()),
		"ts":             numberValue(),
	}, "email", "level", "protocol", "network", "source", "destination", "routeTarget", "originalTarget", "inboundTag", "inboundName", "inboundLocal", "outboundTag", "ts")
	report := object(map[string]*Schema{
		"actionReport": object(map[string]*Schema{
			"blocked":       booleanValue(),
			"ip":            stringValue(),
			"blockDuration": numberValue(),
			"willUnblockAt": stringFormat("date-time"),
			"userId":        stringValue(),
			"processedAt":   stringFormat("date-time"),
		}, "blocked", "ip", "blockDuration", "willUnblockAt", "userId", "processedAt"),
		"xrayReport": webhook,
	}, "actionReport", "xrayReport")
	return responseEnvelope(object(map[string]*Schema{
		"reports": array(report),
	}, "reports"))
}

func validationErrorSchema() *Schema {
	return object(map[string]*Schema{
		"statusCode": numberValue(),
		"message":    stringValue(),
		"errors":     array(record(anyValue()), 1),
	}, "statusCode", "message", "errors")
}

func applicationErrorSchema() *Schema {
	return object(map[string]*Schema{
		"timestamp": stringFormat("date-time"),
		"path":      stringValue(),
		"message":   oneOf(stringValue(), array(stringValue())),
		"errorCode": stringValue(),
	}, "timestamp", "path", "message", "errorCode")
}

func genericHTTPErrorSchema() *Schema {
	return object(map[string]*Schema{
		"statusCode": numberValue(),
		"message":    oneOf(stringValue(), array(stringValue())),
		"error":      stringValue(),
	}, "statusCode", "message")
}
