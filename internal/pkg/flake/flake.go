package flake

import "github.com/bwmarrin/snowflake"

func NewSnowflake() (*snowflake.Node, error) {
	snowflake.Epoch = 1558326573939 // 初音ミク最高です！！！ :D

	return snowflake.NewNode(0)
}
