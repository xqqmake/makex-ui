package controller

import (
	"x-ui/web/service"
	
	"github.com/gin-gonic/gin"
)

type XUIController struct {
	BaseController

	inboundController     *InboundController
	serverController      *ServerController
	settingController     *SettingController
	xraySettingController *XraySettingController
	serverService  service.ServerService
}

func NewXUIController(g *gin.RouterGroup) *XUIController {
	a := &XUIController{}
	a.initRouter(g)
	return a
}

func (a *XUIController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/panel")
	g.Use(a.checkLogin)

	g.GET("/", a.index)
	g.GET("/inbounds", a.inbounds)
	g.GET("/settings", a.settings)
	g.GET("/xray", a.xraySettings)
	g.GET("/navigation", a.navigation)
	g.GET("/nodes", a.nodesPage)  // 节点管理页面

                 // 【新增 2】注册页面路由
	g.GET("/servers", a.serversPage)

	a.inboundController = NewInboundController(g)
	a.serverController = NewServerController(g, a.serverService)
	a.settingController = NewSettingController(g)
	a.xraySettingController = NewXraySettingController(g)
}

func (a *XUIController) index(c *gin.Context) {
	html(c, "index.html", "pages.index.title", nil)
}

func (a *XUIController) inbounds(c *gin.Context) {
	html(c, "inbounds.html", "pages.inbounds.title", nil)
}

func (a *XUIController) settings(c *gin.Context) {
	html(c, "settings.html", "pages.settings.title", nil)
}

func (a *XUIController) xraySettings(c *gin.Context) {
	html(c, "xray.html", "pages.xray.title", nil)
}

func (a *XUIController) navigation(c *gin.Context) {
	html(c, "navigation.html", "pages.navigation.title", nil)
}

// 【新增 4】添加页面渲染方法
func (a *XUIController) serversPage(c *gin.Context) {
	html(c, "servers.html", "pages.controlledmanagement.title", nil)
}

// nodesPage 节点管理页面
func (a *XUIController) nodesPage(c *gin.Context) {
	html(c, "nodes.html", "pages.controlledmanagement.title", nil)
}
