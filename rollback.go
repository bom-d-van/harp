package main

import "fmt"

func lsRollbackVersions(servers []*Server) {
	for _, s := range servers {
		fmt.Println("# ====================================")
		fmt.Println("#", s.String())
		s.initPathes()
		session := s.getSession()
		output, err := session.CombinedOutput(fmt.Sprintf("ls -1 %s/harp/%s/releases/", s.Home, cfg.App.Name))
		if err != nil {
			fmt.Printf("echo $HOME on %s error: %s\n", s, err)
		}
		session.Close()
		fmt.Printf(string(output))
	}
}

func rollback(servers []*Server, version string) {
	for _, s := range servers {
		fmt.Printf("%s rollback start\n", s.String())
		s.initPathes()
		session := s.getSession()
		if debugf {
			fmt.Println(s.retrieveRollbackScript())
		}
		output, err := session.CombinedOutput(fmt.Sprintf("%s/harp/%s/rollback.sh %s", s.Home, cfg.App.Name, version))
		if err != nil {
			fmt.Printf("rollback on %s error: %s\n", s, err)
		}
		session.Close()
		fmt.Printf(string(output))
		fmt.Printf("%s rollback done\n", s.String())
	}
}
