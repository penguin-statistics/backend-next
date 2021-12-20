package main

import (
	"github.com/penguin-statistics/backend-next/cmd/service"
)

// @title          Penguin Statistics API
// @version        3.0.0-alpha.1
// @description    This is the Penguin Statistics v3 API, re-designed to aim for lightweight on wire.
// @contact.name   AlvISs_Reimu
// @contact.email  alvissreimu@gmail.com
// @contact.url    https://github.com/AlvISsReimu
// @license.name   MIT License
// @license.url    https://opensource.org/licenses/MIT
// @host           https://penguin-stats.io
// @BasePath       /api
func main() {
	service.Bootstrap()
}
