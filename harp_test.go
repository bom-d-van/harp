package main

import "testing"

func TestRetrieveServers(t *testing.T) {
	cfg.Servers = map[string][]*Server{
		"prod": {
			&Server{
				User: "app",
				Host: "192.168.59.103",
				Port: ":49153",
			},
			&Server{
				User: "app",
				Host: "192.168.59.103",
				Port: ":49154",
			},
		},
		"dev": {
			&Server{
				User: "app",
				Host: "192.168.59.103",
				Port: ":49155",
			},
			&Server{
				User: "app",
				Host: "192.168.59.103",
				Port: ":49156",
			},
		},
	}
	option.serverSets = []string{"prod"}
	option.servers = []string{"app@192.168.59.103:49156"}
	servers := retrieveServers()
	if len(servers) != 3 {
		t.Error("failed to retrieve 3 correct servers")
	}
	for i, server := range servers {
		switch i {
		case 0:
			if server.String() != "app@192.168.59.103:49153" {
				t.Error("failed to retrieve server app@192.168.59.103:49153")
			}
		case 1:
			if server.String() != "app@192.168.59.103:49154" {
				t.Error("failed to retrieve server app@192.168.59.103:49154")
			}
		case 2:
			if server.String() != "app@192.168.59.103:49156" {
				t.Error("failed to retrieve server app@192.168.59.103:49156")
			}
		}
	}
}
