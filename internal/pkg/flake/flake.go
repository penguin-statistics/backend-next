package flake

import "github.com/bwmarrin/snowflake"

func NewSnowflake() (*snowflake.Node, error) {
	snowflake.Epoch = 1558326573939 // 初音ミク最高です！！！ :D

	// TODO: change 0 to the actual node ID
	return snowflake.NewNode(0)
}
