package main

import (
	"github.com/penguin-statistics/backend-next/cmd/service"
)

// @title        Penguin Statistics API
// @version           3.0.0
// @description  This is the Penguin Statistics v3 Backend, implemented for best performance, scalability, and reliability.

// @contact.name   AlvISs_Reimu
// @contact.email  alvissreimu@gmail.com
// @contact.url    https://github.com/AlvISsReimu

// @license.name  MIT License
// @license.url   https://opensource.org/licenses/MIT

// @host      penguin-stats.io
// @schemes   https
// @BasePath  /

// @securityDefinitions.apikey  PenguinIDAuth
// @in                          header
// @name                        Authorization

func main() {
	service.Bootstrap()
}
