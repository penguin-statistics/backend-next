package appcontext

const (
	EnvServer Env = iota
	EnvWorker
	EnvCLI
)

type Env int

type Ctx struct {
	Env Env
}

func Declare(env Env) Ctx {
	return Ctx{
		Env: env,
	}
}
