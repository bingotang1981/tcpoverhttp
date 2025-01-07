package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"tcp-over-http/constant"
)

var log *zerolog.Logger
var keyStr string

type config struct {
	Port   int
	Path   string
	KeyStr string
}

func RegisterLogger(logger *zerolog.Logger) {
	log = logger
}

func RegisterCmd(root *cobra.Command) {
	cfg := &config{}
	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "proxy server mode",
		Run: func(cmd *cobra.Command, args []string) {
			run(cmd.Context(), cfg)
		},
	}
	serverCmd.Flags().IntVarP(&cfg.Port, "port", "", 80, "listen port")
	serverCmd.Flags().StringVarP(&cfg.Path, "path", "p", "/proxy", "proxy http path")
	serverCmd.Flags().StringVarP(&cfg.KeyStr, "keyStr", "k", "", "key, example: 123456")
	root.AddCommand(serverCmd)
}

func run(ctx context.Context, cfg *config) {
	
	keyStr = cfg.KeyStr
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.GET("/healthz", healthHandler)
	engine.POST("/"+strings.TrimLeft(cfg.Path, "/"), proxyHandler)
	
	go startMonitorThread()

	serv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port), Handler: engine}
	ctxServ, cancel := context.WithCancel(context.Background())

	go func() {
		defer cancel()

		log.Info().Msgf("proxy server listen at: %d", cfg.Port)
		if err := serv.ListenAndServe(); err != nil {
			log.Error().Err(err).Msgf("proxy server listen failed")
		}
	}()

	select {
	case <-ctx.Done():
		_ = serv.Shutdown(context.Background())
		log.Info().Msgf("proxy server shutdown")
	case <-ctxServ.Done():
	}
}

func healthHandler(c *gin.Context) {
	c.String(http.StatusOK, "ok")
}

func proxyHandler(c *gin.Context) {
	defer c.Request.Body.Close()

	id := c.GetHeader("Proxy-Id")
	action := c.GetHeader("Proxy-Action")
	itemId := c.GetHeader("Proxy-ItemId")

	if id == "" {
		c.Data(http.StatusOK, "application/octet-stream", []byte{constant.NO_ID})
		return
	}

	switch action {
	case constant.Establish:
		rawTarget, err := getRawTarget(c)
		if err != nil {
			c.Data(http.StatusOK, "application/octet-stream", []byte{constant.ERROR})
			return
		}

		if len(rawTarget) == 0 {
			c.Data(http.StatusOK, "application/octet-stream", []byte{constant.ERROR})
			return
		}

		ad, err := newAdapter(id, rawTarget, keyStr)
		if err != nil {
			c.Data(http.StatusOK, "application/octet-stream", []byte{constant.ERROR})
			return
		}
		c.Data(http.StatusOK, "application/octet-stream", []byte{constant.NORMAL})
		log.Info().Str("id", ad.id).Msgf("new connection established for %s", ad.target)

	case constant.Read:
		ad := getAdapter(id)
		if ad == nil {
			c.Data(http.StatusOK, "application/octet-stream", []byte{constant.NO_ID})
			return
		}
		ad.read(c, itemId)

	case constant.Write:
		ad := getAdapter(id)
		if ad == nil {
			c.Data(http.StatusOK, "application/octet-stream", []byte{constant.NO_ID})
			return
		}
		ad.write(c, itemId)

	case constant.Goodbye:
		deleteAdapter(id)
		c.Data(http.StatusOK, "application/octet-stream", []byte{constant.NORMAL})
		log.Info().Str("id", id).Msgf("proxy finish")
	default:
		c.Data(http.StatusOK, "application/octet-stream", []byte{constant.ERROR})
	}
}

func startMonitorThread() {
	for {
		//heartbeat interval is 90 seconds as 100 seconds is the default timeout in cloudflare cdn
		time.Sleep(constant.HEART_BEAT_INTERVAL * time.Second)
		monitorConnHeartbeat()
	}
}
