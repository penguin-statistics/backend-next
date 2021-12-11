package controllers

import (
	"encoding/hex"
	"log"
	"math/rand"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/penguin-statistics/backend-next/internal/models/protos"
	"github.com/penguin-statistics/backend-next/internal/server"
	"google.golang.org/protobuf/proto"
)

type LiveController struct {
}

func RegisterLiveController(v3 *server.V3) {
	c := &LiveController{}

	v3.Get("/live", c.Live())
}

func (c *LiveController) Live() func(ctx *fiber.Ctx) error {
	return websocket.New(func(c *websocket.Conn) {
		defer c.Close()

		_, b, err := c.ReadMessage()
		if err != nil {
			log.Println(err)
			return
		}

		log.Println(hex.EncodeToString(b))

		elements := []*protos.MatrixUpdateMessage_Segment_Element{}
		for i := 0; i < 25; i++ {
			elements = append(elements, &protos.MatrixUpdateMessage_Segment_Element{
				Id: &protos.MatrixUpdateMessage_Segment_Element_StageId{
					StageId: int32(rand.Intn(100)),
				},
				Amount: int32(rand.Intn(1000000)),
			})
		}

		segments := []*protos.MatrixUpdateMessage_Segment{}
		for i := 0; i < 3; i++ {
			segments = append(segments, &protos.MatrixUpdateMessage_Segment{
				Bucket: &protos.MatrixUpdateMessage_Segment_ItemId{
					ItemId: int32(rand.Intn(500)),
				},
				Elements: elements,
			})
		}

		msg := protos.MatrixUpdateMessage{
			Header: &protos.Header{
				Type: protos.MessageType_MATRIX_ABSOLUTE_UPDATE_MESSAGE,
			},
			Segments: segments,
		}

		res, err := proto.Marshal(&msg)
		if err != nil {
			log.Println(err)
			return
		}

		c.WriteMessage(websocket.BinaryMessage, res)

		// writer, err := c.NextWriter(websocket.BinaryMessage)
		// if err != nil {
		// 	log.Println(err)
		// 	return
		// }

		// gw := gzip.NewWriter(writer)

		// _, err = gw.Write(res)
		// if err != nil {
		// 	log.Println(err)
		// 	return
		// }

		// gw.Close()
		// writer.Close()

		time.Sleep(time.Millisecond * 100)

		err = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second*5))
		if err != nil {
			log.Println(err)
			return
		}
	}, websocket.Config{
		Subprotocols:      []string{"v3.penguin-stats.live+proto"},
		EnableCompression: true,
	})
}
