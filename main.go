package main

import (
	"exusiai.dev/backend-next/cmd/app"
)

//	@title			Penguin Statistics API
//	@version		3.0.0
//	@description	Public API of Penguin Statistics (penguin-stats.io): crowdsourced Arknights item drop statistics for the CN, US, JP and KR game servers. Reading the data needs no key; send a User-Agent that names your program and cache responses for at least 5 minutes. Drop rate = quantity / times (items per run, can exceed 1); always pass `server` (CN when omitted). Data: CC BY-NC 4.0, credit "Data: Penguin Statistics (penguin-stats.io), CC BY-NC 4.0"; source code: MIT. Guide for agents: https://penguin-stats.io/llms.txt

//	@contact.name	AlvISs_Reimu
//	@contact.email	alvissreimu@gmail.com
//	@contact.url	https://github.com/AlvISsReimu

//	@license.name	CC BY-NC 4.0 (data)
//	@license.url	https://creativecommons.org/licenses/by-nc/4.0/

//	@host		penguin-stats.io
//	@schemes	https
//	@BasePath	/

//	@securityDefinitions.apikey	PenguinIDAuth
//	@in							header
//	@name						Authorization

func main() {
	app.Run()
}
