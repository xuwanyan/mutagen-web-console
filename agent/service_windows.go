//go:build windows

package main

import (
	"log"

	"mutagen-web/agent/client"
	"golang.org/x/sys/windows/svc"
)

// runAsService 以 Windows 服务模式运行 agent
// 返回 true 表示已作为服务运行（主流程应退出）
// 返回 false 表示不是服务模式（主流程正常启动）
func runAsService(cfg *AgentConfig, configPath string, saver func(token, machineID string) error) bool {
	isService, err := svc.IsWindowsService()
	if err != nil {
		log.Printf("svc check error: %v", err)
		return false
	}
	if !isService {
		return false
	}

	log.Printf("running as Windows service: MutagenAgent")

	err = svc.Run("MutagenAgent", &agentService{
		cfg:        cfg,
		configPath: configPath,
		saver:      saver,
	})
	if err != nil {
		log.Fatalf("service failed: %v", err)
	}
	return true
}

// agentService 实现 svc.Handler
type agentService struct {
	cfg        *AgentConfig
	configPath string
	saver      func(token, machineID string) error
}

func (s *agentService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown

	changes <- svc.Status{State: svc.StartPending}

	agent, err := client.NewAgent(
		s.cfg.ServerURL,
		s.cfg.Token,
		s.cfg.MachineID,
		s.cfg.Name,
		s.configPath,
		s.saver,
	)
	if err != nil {
		log.Printf("create agent failed: %v", err)
		changes <- svc.Status{State: svc.Stopped}
		return true, 1
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run()
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	for {
		select {
		case err := <-errCh:
			if err != nil {
				log.Printf("agent error: %v", err)
			}
			changes <- svc.Status{State: svc.Stopped}
			return false, 0

		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				log.Println("service stop requested")
				agent.Close()
				changes <- svc.Status{State: svc.StopPending}
				return false, 0
			default:
				continue
			}
		}
	}
}