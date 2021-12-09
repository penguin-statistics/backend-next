package controllers

import (
	"compress/gzip"
	"encoding/hex"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/penguin-statistics/backend-next/internal/models/protos"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/utils"
	"google.golang.org/protobuf/proto"

	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
)

type ItemController struct {
	repo  *repos.ItemRepo
	redis *redis.Client
}

func RegisterItemController(v3 *server.V3, repo *repos.ItemRepo, redis *redis.Client) {
	c := &ItemController{
		repo:  repo,
		redis: redis,
	}

	v3.Get("/items", c.GetItems)
	v3.Get("/items/:itemId", buildSanitizer(utils.NonNullString, utils.IsInt), c.GetItemById)

	v3.Get("/live", websocket.New(func(c *websocket.Conn) {
		defer c.Close()

		_, b, err := c.ReadMessage()
		if err != nil {
			log.Println(err)
			return
		}

		log.Println(hex.EncodeToString(b))

		elements := []*protos.MatrixUpdateMessage_Segment_Element{}
		for i := 0; i < 5; i++ {
			elements = append(elements, &protos.MatrixUpdateMessage_Segment_Element{
				Id: &protos.MatrixUpdateMessage_Segment_Element_StageId{
					StageId: rand.Int31(),
				},
				Amount: rand.Int31(),
			})
		}

		segments := []*protos.MatrixUpdateMessage_Segment{}
		for i := 0; i < 40; i++ {
			segments = append(segments, &protos.MatrixUpdateMessage_Segment{
				Bucket: &protos.MatrixUpdateMessage_Segment_ItemId{
					ItemId: rand.Int31(),
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

		writer, err := c.NextWriter(websocket.BinaryMessage)
		if err != nil {
			log.Println(err)
			return
		}

		gw := gzip.NewWriter(writer)

		_, err = gw.Write(res)
		if err != nil {
			log.Println(err)
			return
		}

		gw.Close()
		writer.Close()

		time.Sleep(time.Millisecond * 100)

		err = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second*5))
		if err != nil {
			log.Println(err)
			return
		}
	}, websocket.Config{
		Subprotocols:      []string{"penguin-matrix"},
		EnableCompression: true,
	}))
}

func buildSanitizer(sanitizer ...func(string) bool) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		itemId := strings.TrimSpace(ctx.Params("itemId"))

		for _, sanitizer := range sanitizer {
			if !sanitizer(itemId) {
				return fiber.NewError(http.StatusBadRequest, "invalid or missing itemId")
			}
		}

		return ctx.Next()
	}
}

// GetItems godoc
// @Summary      Get all Items
// @Description  Get all Items
// @Tags         Item
// @Produce      json
// @Success      200     {array}  models.PItem{name=models.I18nString,existence=models.Existence,keywords=models.Keywords}
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /v3/items [GET]
func (c *ItemController) GetItems(ctx *fiber.Ctx) error {
	items, err := c.repo.GetItems(ctx.Context())
	if err != nil {
		return err
	}

	return ctx.JSON(items)
}

// GetItemById godoc
// @Summary      Get an Item with numerical ID
// @Description  Get an Item using the item's numerical ID
// @Tags         Item
// @Produce      json
// @Param        itemId  path      int  true  "Numerical Item ID"
// @Success      200     {object}  models.PItem{name=models.I18nString,existence=models.Existence,keywords=models.Keywords}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing itemId. Notice that this shall be the **numerical ID** of the item, instead of the previously used string form **arkItemId** of the item."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /v3/items/{itemId} [GET]
func (c *ItemController) GetItemById(ctx *fiber.Ctx) error {
	itemId := ctx.Params("itemId")

	item, err := c.repo.GetItemById(ctx.Context(), itemId)
	if err != nil {
		return err
	}

	return ctx.JSON(item)
}
