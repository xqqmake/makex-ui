package web

import (
	"context"
	"crypto/tls"
	"embed"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"x-ui/config"
	"x-ui/logger"
	"x-ui/util/common"
	"x-ui/web/controller"
    	"x-ui/web/job"
	"x-ui/web/locale"
	"x-ui/web/middleware"
	"x-ui/web/network"
	"x-ui/web/service"

	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
)

//go:embed assets/*
var assetsFS embed.FS

//go:embed html/*
var htmlFS embed.FS

//go:embed translation/*
var i18nFS embed.FS

var startTime = time.Now()

// 预定义 IPv4 私网和回环网段
var privateIPv4Nets []*net.IPNet

type wrapAssetsFS struct {
	embed.FS
}

// Keep-Alive 监听器包装器：用于拦截新连接并设置 Keep-Alive 选项
type keepAliveListener struct {
	*net.TCPListener
	KeepAlivePeriod time.Duration
}

// Accept 方法：拦截连接并设置 Keep-Alive
func (l keepAliveListener) Accept() (net.Conn, error) {
	// 1. 接受底层 TCP 连接
	tc, err := l.TCPListener.AcceptTCP()
	if err != nil {
		return nil, err
	}
	
	// 2. 在 *net.TCPConn 上设置 Keep-Alive 属性 (这里的方法是正确的)
	if err := tc.SetKeepAlive(true); err != nil {
		logger.Warning("Failed to set KeepAlive:", err)
	}
	// 设置心跳包周期为 5 秒
	if err := tc.SetKeepAlivePeriod(l.KeepAlivePeriod); err != nil {
		logger.Warning("Failed to set KeepAlivePeriod:", err)
	}
	
	return tc, nil
}

func (f *wrapAssetsFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open("assets/" + name)
	if err != nil {
		return nil, err
	}
	return &wrapAssetsFile{
		File: file,
	}, nil
}

type wrapAssetsFile struct {
	fs.File
}

func (f *wrapAssetsFile) Stat() (fs.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return &wrapAssetsFileInfo{
		FileInfo: info,
	}, nil
}

type wrapAssetsFileInfo struct {
	fs.FileInfo
}

func (f *wrapAssetsFileInfo) ModTime() time.Time {
	return startTime
}

type Server struct {
	httpServer *http.Server
	listener   net.Listener

	index  *controller.IndexController
	server *controller.ServerController
	panel  *controller.XUIController
	api    *controller.APIController

	xrayService    service.XrayService
	singboxService service.SingBoxService
	argoService    service.ArgoService
	settingService service.SettingService
	tgbotService    service.TelegramService
	// 〔中文注释〕: 添加这个字段，用来“持有”从 main.go 传递过来的 serverService 实例。
	serverService  service.ServerService

	cron *cron.Cron

	ctx    context.Context
	cancel context.CancelFunc
}


// 【新增方法】：用于 main.go 将创建好的 tgBotService 注入进来
func (s *Server) SetTelegramService(tgService service.TelegramService) {
    s.tgbotService = tgService
}

// 〔中文注释〕: 1. 让 NewServer 能够接收一个 serverService 实例作为参数。
func NewServer(serverService service.ServerService) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		ctx:    ctx,
		cancel: cancel,
		// 〔中文注释〕: 2. 将传入的 serverService 存储到 Server 结构体的字段中。
		serverService: serverService,
	}
}


func (s *Server) getHtmlFiles() ([]string, error) {
	files := make([]string, 0)
	dir, _ := os.Getwd()
	err := fs.WalkDir(os.DirFS(dir), "web/html", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (s *Server) getHtmlTemplate(funcMap template.FuncMap) (*template.Template, error) {
    // 这里用 htmlFS（//go:embed html/*）而不是“templates”
    t := template.New("").Funcs(funcMap)

    // 递归遍历 embed 的 html 目录，解析所有 .html 模板
    err := fs.WalkDir(htmlFS, "html", func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return err
        }
        if d.IsDir() {
            return nil
        }
        if !strings.HasSuffix(path, ".html") {
            return nil
        }

        // 读出模板内容
        b, err := htmlFS.ReadFile(path)
        if err != nil {
            return err
        }

        // 去掉前缀“html/”，让 {{template "form/inbound"}} 这种名字能被正确找到
        name := strings.TrimPrefix(path, "html/")
        _, err = t.New(name).Parse(string(b))
        return err
    })
    if err != nil {
        return nil, err
    }
    return t, nil
}


func (s *Server) initRouter() (*gin.Engine, error) {
	if config.IsDebug() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.DefaultWriter = io.Discard
		gin.DefaultErrorWriter = io.Discard
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.Default()

	webDomain, err := s.settingService.GetWebDomain()
	if err != nil {
		return nil, err
	}

	if webDomain != "" {
		engine.Use(middleware.DomainValidatorMiddleware(webDomain))
	}

	secret, err := s.settingService.GetSecret()
	if err != nil {
		return nil, err
	}

	basePath, err := s.settingService.GetBasePath()
	if err != nil {
		return nil, err
	}
	engine.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{basePath + "panel/api/"})))
	assetsBasePath := basePath + "assets/"

	store := cookie.NewStore(secret)
	engine.Use(sessions.Sessions("makex-ui", store))
	engine.Use(func(c *gin.Context) {
		c.Set("base_path", basePath)
	})
	engine.Use(func(c *gin.Context) {
		uri := c.Request.RequestURI
		if strings.HasPrefix(uri, assetsBasePath) {
			c.Header("Cache-Control", "max-age=31536000")
		}
	})

	// init i18n
	err = locale.InitLocalizer(i18nFS, &s.settingService)
	if err != nil {
		return nil, err
	}

	// Apply locale middleware for i18n
	i18nWebFunc := func(key string, params ...string) string {
		return locale.I18n(locale.Web, key, params...)
	}
	engine.FuncMap["i18n"] = i18nWebFunc
	engine.Use(locale.LocalizerMiddleware())

	// set static files and template
	if config.IsDebug() {
		// for development
		files, err := s.getHtmlFiles()
		if err != nil {
			return nil, err
		}
		engine.LoadHTMLFiles(files...)
		engine.StaticFS(basePath+"assets", http.FS(os.DirFS("web/assets")))
	} else {
		// for production
		template, err := s.getHtmlTemplate(engine.FuncMap)
		if err != nil {
			return nil, err
		}
		engine.SetHTMLTemplate(template)
		engine.StaticFS(basePath+"assets", http.FS(&wrapAssetsFS{FS: assetsFS}))
	}

	// Apply the redirect middleware (`/xui` to `/panel`)
	engine.Use(middleware.RedirectMiddleware(basePath))

	g := engine.Group(basePath)

	s.index = controller.NewIndexController(g)
	// 〔中文注释〕: 调用我们刚刚改造过的 NewServerController，并将 s.serverService 作为参数传进去。
	s.server = controller.NewServerController(g, s.serverService)
	s.panel = controller.NewXUIController(g)
	s.api = controller.NewAPIController(g)

	return engine, nil
}

func (s *Server) startTask() {
	err := s.xrayService.RestartXray(true)
	if err != nil {
		logger.Warning("start xray failed:", err)
	}
	// 双内核：启动 sing-box 进程（无 anytls/tuic/naive 入站时自动跳过）
	err = s.singboxService.RestartSingBox(true)
	if err != nil {
		logger.Warning("start sing-box failed:", err)
	}
	// Argo 隧道：启动 cloudflared 快速隧道（无启用 Argo 的 ws 入站时自动跳过）
	err = s.argoService.RestartArgo()
	if err != nil {
		logger.Warning("start argo tunnel failed:", err)
	}
	// Check whether xray is running every second
	s.cron.AddJob("@every 1s", job.NewCheckXrayRunningJob())
	// 双内核：检查 sing-box 进程是否在运行
	s.cron.AddJob("@every 1s", job.NewCheckSingBoxRunningJob())
	// Argo：检查 cloudflared 隧道进程是否在运行
	s.cron.AddJob("@every 1s", job.NewCheckArgoRunningJob())

	// Check if xray needs to be restarted every 30 seconds
	s.cron.AddFunc("@every 30s", func() {
		if s.xrayService.IsNeedRestartAndSetFalse() {
			err := s.xrayService.RestartXray(false)
			if err != nil {
				logger.Error("restart xray failed:", err)
			}
		}
		// 双内核：anytls/tuic/naive 入站变更后重启 sing-box
		if s.singboxService.IsNeedRestartAndSetFalse() {
			err := s.singboxService.RestartSingBox(false)
			if err != nil {
				logger.Error("restart sing-box failed:", err)
			}
		}
		// Argo：入站变更后同步 cloudflared 隧道
		if s.argoService.IsNeedRestartAndSetFalse() {
			err := s.argoService.RestartArgo()
			if err != nil {
				logger.Error("restart argo tunnel failed:", err)
			}
		}
	})

	go func() {
		time.Sleep(time.Second * 5)
		// Statistics every 10 seconds, start the delay for 5 seconds for the first time, and staggered with the time to restart xray
		s.cron.AddJob("@every 10s", job.NewXrayTrafficJob())
		// 双内核：sing-box(anytls/tuic/naive) 流量统计，与 xray 统计错开间隔
		s.cron.AddJob("@every 10s", job.NewSingBoxTrafficJob())
	}()

	// check client ips from log file every 10 sec
	//	s.cron.AddJob("@every 10s", job.NewCheckClientIpJob())

	// check client ips from log file every day
	s.cron.AddJob("@daily", job.NewClearLogsJob())

	// Make a traffic condition every day, 8:30
	var entry cron.EntryID
	isTgbotenabled, err := s.settingService.GetTgbotEnabled()
	if (err == nil) && (isTgbotenabled) {
		runtime, err := s.settingService.GetTgbotRuntime()
		if err != nil || runtime == "" {
			logger.Errorf("Add NewStatsNotifyJob error[%s], Runtime[%s] invalid, will run default", err, runtime)
			runtime = "@daily"
		}
		logger.Infof("Tg notify enabled,run at %s", runtime)
		_, err = s.cron.AddJob(runtime, job.NewStatsNotifyJob())
		if err != nil {
			logger.Warning("Add NewStatsNotifyJob error", err)
			return
		}

		// check for Telegram bot callback query hash storage reset
		s.cron.AddJob("@every 2m", job.NewCheckHashStorageJob())

		// Check CPU load and alarm to TgBot if threshold passes
		cpuThreshold, err := s.settingService.GetTgCpu()
		if (err == nil) && (cpuThreshold > 0) {
			s.cron.AddJob("@every 10s", job.NewCheckCpuJob())
		}
	} else {
		s.cron.Remove(entry)
	}
}

func (s *Server) Start() (err error) {
	// This is an anonymous function, no function name
	defer func() {
		if err != nil {
			s.Stop()
		}
	}()

	loc, err := s.settingService.GetTimeLocation()
	if err != nil {
		return err
	}
	s.cron = cron.New(cron.WithLocation(loc), cron.WithSeconds())
	s.cron.Start()

	engine, err := s.initRouter()
	if err != nil {
		return err
	}

	certFile, err := s.settingService.GetCertFile()
	if err != nil {
		return err
	}
	keyFile, err := s.settingService.GetKeyFile()
	if err != nil {
		return err
	}
	listen, err := s.settingService.GetListen()
	if err != nil {
		return err
	}
	port, err := s.settingService.GetPort()
	if err != nil {
		return err
	}
	var listenAddr string

	if certFile != "" && keyFile != "" {
		// 方式一：配置了证书，启用 HTTPS
		// 检查证书是否有效，如果无效则直接报错退出，不允许回退到 HTTP
		_, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			logger.Errorf("Error loading certificates, please check the file path and content: %v", err)
			return err
		}
		// 监听用户配置的地址
		listenAddr = net.JoinHostPort(listen, strconv.Itoa(port))
	} else {
		// 方式二：未配置证书，强制监听在本地回环地址，仅供 SSH 转发使用
		logger.Info("No certificate configured. Forcing listen address to localhost for security.")
		logger.Info("Access is only possible via SSH tunnel (e.g., http://127.0.0.1).")
		
		// 无论用户在 listen 中填写什么，都强制使用回环地址
		listen = fallbackToLocalhost(listen)
		listenAddr = net.JoinHostPort(listen, strconv.Itoa(port))
	}

	 // 1. 使用 baseListener 临时变量接收 net.Listen 的结果，这是底层的 TCP 监听器
     baseListener, err := net.Listen("tcp", listenAddr)
     if err != nil {
	    return err
    }

     // 声明最终要使用的 listener 变量
     var listener net.Listener

     // 2. 尝试将 net.Listener 断言为 *net.TCPListener，以便进行更底层的设置
     tcpListener, ok := baseListener.(*net.TCPListener)
     if !ok {
	    // 如果断言失败 (例如在某些特殊环境或测试中)，则直接使用原始的 listener，不设置 Keep-Alive
	      logger.Warning("监听器不是 TCPListener 类型, 无法设置 Keep-Alive。")
	      listener = baseListener
     } else {
	 // 3. 【核心功能】: 使用自定义的包装器为每一个新的连接设置 Keep-Alive 属性
	 kaListener := &keepAliveListener{
	 TCPListener:     tcpListener,
	 KeepAlivePeriod: 5 * time.Second, // 将 Keep-Alive 探测周期设置为 5 秒
	 }
	 // 将包装后的监听器赋值给最终的 listener 变量，后续流程将使用这个新的 listener
	 listener = net.Listener(kaListener)
     }

	// 再次检查证书，配置 TLS Listener
	if certFile != "" && keyFile != "" {
		cert, _ := tls.LoadX509KeyPair(certFile, keyFile) // 这里我们忽略错误，因为上面已经检查过了
		c := &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
		listener = network.NewAutoHttpsListener(listener)
		listener = tls.NewListener(listener, c)
		logger.Info("Web server running HTTPS on", listener.Addr())
	} else {
		logger.Info("Web server running HTTP on", listener.Addr())
	}
	s.listener = listener

	// 修改 s.httpServer 的初始化代码
	s.httpServer = &http.Server{
		Handler: engine,
		// 【新增】：设置 120 秒的读写超时，确保 ufw 命令有足够的时间完成
		ReadTimeout:  120 * time.Second, 
		WriteTimeout: 120 * time.Second, 
	}


	go func() {
		s.httpServer.Serve(listener)
	}()

	s.startTask()

	// 启动 TG Bot
    isTgbotenabled, err := s.settingService.GetTgbotEnabled()
    if (err == nil) && (isTgbotenabled) {
        // 现在直接在注入的实例上调用 Start 方法，而不是 NewTgbot()
        // 因为 main.go 已经注入了完整的实例
        if tgbot, ok := s.tgbotService.(*service.Tgbot); ok {
            tgbot.Start(i18nFS)
        } else {
            logger.Warning("Telegram Bot 已启用，但注入的实例类型不正确或为 nil，无法启动。")
        }
    }

	return nil
}

func (s *Server) Stop() error {
	s.cancel()
	s.xrayService.StopXray()
	s.singboxService.StopSingBox()
	s.argoService.StopArgo()
	if s.cron != nil {
		s.cron.Stop()
	}
	// 只有在断言成功后，才能调用只在 *service.Tgbot 上定义的 Stop() 和 IsRunning() 方法。
	if tgBot, ok := s.tgbotService.(*service.Tgbot); ok {
		if tgBot.IsRunning() {
			tgBot.Stop()
		}
	}
	var err1 error
	var err2 error
	if s.httpServer != nil {
		err1 = s.httpServer.Shutdown(s.ctx)
	}
	if s.listener != nil {
		err2 = s.listener.Close()
	}
	return common.Combine(err1, err2)
}

func (s *Server) GetCtx() context.Context {
	return s.ctx
}

func (s *Server) GetCron() *cron.Cron {
	return s.cron
}

// isInternalIP 判断是否为私网或回环IP（支持IPv4和IPv6）
func isInternalIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	if ip4 := ip.To4(); ip4 != nil {
		// IPv4 判断是否在私网/回环网段内
		for _, privateNet := range privateIPv4Nets {
			if privateNet.Contains(ip4) {
				return true
			}
		}
		return false
	}

	// IPv6 判断回环或链路本地地址
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return true
	}

	// 判断 IPv6 fc00::/7 私网地址段
	if ip[0]&0xfe == 0xfc {
		return true
	}

	return false
}

// fallbackToLocalhost 根据传入地址返回对应的本地回环地址
func fallbackToLocalhost(listen string) string {
	ip := net.ParseIP(listen)
	if ip == nil {
		// 无法解析则默认回退 IPv4 回环
		return "127.0.0.1"
	}
	if ip.To4() != nil {
		// IPv4 回退 IPv4 回环
		return "127.0.0.1"
	}
	// IPv6 回退 IPv6 回环
	return "::1"
}

func init() {
	for _, cidr := range []string{
		"10.0.0.0/8",     // A类私网
		"172.16.0.0/12",  // B类私网
		"192.168.0.0/16", // C类私网
		"100.64.0.0/10",  // CGNAT地址段
		"127.0.0.0/8",    // 回环
	} {
		_, netw, err := net.ParseCIDR(cidr)
		if err == nil {
			privateIPv4Nets = append(privateIPv4Nets, netw)
		}
	}
}
