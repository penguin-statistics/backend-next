package service

import (
	"context"
	"math"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/goccy/go-json"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
)

// SEOSourceCategory is the source category of the matrix the stats pages show: the one the frontend shows by
// default (frontend-v2 src/store/modules/dataSource.js).
const SEOSourceCategory = constant.SourceCategoryAll

// SEOCatalogRefreshInterval is how often SEOData reloads the items, stages and zones.
const SEOCatalogRefreshInterval = time.Minute

// seoWarmTimeout bounds the calculation of the matrix of a server
const seoWarmTimeout = 10 * time.Minute

// seoWarmInterval is how often SEOData calculates the matrices that are not cached: after a start, after a failed
// calculation, and after a cached matrix expired or was dropped by a failed refresh, which the worker redoes only in
// the process that holds its lock
const seoWarmInterval = 5 * time.Minute

// SEOData is the data of the server-rendered stats pages. It never waits for the database: the items, stages and
// zones are reloaded in the background, and the matrix is the cached global matrix of the frontend, which the calc
// worker keeps up to date.
type SEOData struct {
	itemService       *Item
	stageService      *Stage
	zoneService       *Zone
	dropMatrixService *DropMatrix

	catalog atomic.Pointer[SEOCatalog]
	// catalogInputs are the items, stages and zones the catalog was built from; only run writes them
	catalogInputs seoCatalogInputs

	matricesMu sync.Mutex
	matrices   map[string]*seoMatrixMemo

	// calcMatrix calculates and caches the matrix of a server; nil calculates it with dropMatrixService
	calcMatrix func(ctx context.Context, server string) error
	// warming is set while a pass of warmMatrices runs
	warming atomic.Bool
	// lastWarm is when the last pass of warmMatrices started; only maybeWarm reads and writes it
	lastWarm time.Time
}

func NewSEOData(lc fx.Lifecycle, conf *appconfig.Config, itemService *Item, stageService *Stage, zoneService *Zone, dropMatrixService *DropMatrix) *SEOData {
	d := &SEOData{
		itemService:       itemService,
		stageService:      stageService,
		zoneService:       zoneService,
		dropMatrixService: dropMatrixService,
		matrices:          make(map[string]*seoMatrixMemo),
	}
	if conf.SEOPagesEnabled {
		ctx, cancel := context.WithCancel(context.Background())
		lc.Append(fx.Hook{
			OnStart: func(context.Context) error {
				go d.run(ctx)
				return nil
			},
			OnStop: func(context.Context) error {
				cancel()
				return nil
			},
		})
	}
	return d
}

func (d *SEOData) run(ctx context.Context) {
	ticker := time.NewTicker(SEOCatalogRefreshInterval)
	defer ticker.Stop()
	for {
		if err := d.RefreshCatalog(ctx); err != nil && ctx.Err() == nil {
			log.Warn().
				Str("evt.name", "seo.catalog.refresh_failed").
				Err(err).
				Msg("failed to reload the items, stages and zones of the stats pages; keeping the ones at hand")
		} else if err == nil {
			d.maybeWarm(ctx, time.Now())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// maybeWarm starts a pass of warmMatrices in the background, unless one runs or the last one started less than
// seoWarmInterval before now. It is not safe for concurrent use.
func (d *SEOData) maybeWarm(ctx context.Context, now time.Time) {
	if !d.lastWarm.IsZero() && now.Sub(d.lastWarm) < seoWarmInterval {
		return
	}
	if !d.warming.CompareAndSwap(false, true) {
		return
	}
	d.lastWarm = now
	go func() {
		defer d.warming.Store(false)
		d.warmMatrices(ctx)
	}()
}

// warmMatrices calculates the cached matrix of each server that is not cached, one server at a time, the way a
// visit to the app does: the pages of items and stages are 503s until then, after a start, a failed calculation or
// the expiry of the cached matrix, and the worker refreshes the matrices only in the process that holds its lock.
func (d *SEOData) warmMatrices(ctx context.Context) {
	calc := d.calcMatrix
	if calc == nil {
		calc = func(ctx context.Context, server string) error {
			_, err := d.dropMatrixService.GetShimDropMatrix(ctx, server, true, "", "", null.Int{}, SEOSourceCategory)
			return err
		}
	}
	for _, server := range constant.Servers {
		if ctx.Err() != nil {
			return
		}
		if d.Matrix(server) != nil {
			continue
		}
		start := time.Now()
		wctx, cancel := context.WithTimeout(ctx, seoWarmTimeout)
		err := calc(wctx, server)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				log.Warn().
					Str("evt.name", "seo.matrix.warm_failed").
					Err(err).
					Str("server", server).
					Msg("failed to calculate the matrix of the stats pages; retrying in the next pass, or a visit to the app calculates it")
			}
			continue
		}
		log.Info().
			Str("evt.name", "seo.matrix.warmed").
			Str("server", server).
			Dur("duration", time.Since(start)).
			Msg("calculated the matrix of the stats pages")
	}
}

// RefreshCatalog reloads the items, stages and zones, and replaces the catalog if they changed. It is not safe for
// concurrent use.
func (d *SEOData) RefreshCatalog(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	items, err := d.itemService.GetItems(ctx)
	if err != nil {
		return err
	}
	stages, err := d.stageService.GetStages(ctx)
	if err != nil {
		return err
	}
	zones, err := d.zoneService.GetZones(ctx)
	if err != nil {
		return err
	}
	inputs := seoCatalogInputs{items: items, stages: stages, zones: zones}
	if d.catalog.Load() != nil && inputs.same(d.catalogInputs) {
		// the pages rendered from the catalog at hand stay valid (the lists are rendered once per catalog)
		return nil
	}
	d.catalogInputs = inputs
	d.catalog.Store(NewSEOCatalog(items, stages, zones))
	return nil
}

// seoCatalogInputs are the rows a catalog is built from
type seoCatalogInputs struct {
	items  []*model.Item
	stages []*model.Stage
	zones  []*model.Zone
}

// same tells whether i and o hold the same rows: the same cached slices, or rows equal to them once the caches of
// the items, stages and zones expired and they were loaded again
func (i seoCatalogInputs) same(o seoCatalogInputs) bool {
	return sameRows(i.items, o.items) && sameRows(i.stages, o.stages) && sameRows(i.zones, o.zones)
}

func sameRows[T any](a, b []*T) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 || &a[0] == &b[0] {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// Catalog returns the items, stages and zones, or nil before they are loaded.
func (d *SEOData) Catalog() *SEOCatalog {
	return d.catalog.Load()
}

// Matrix returns the global matrix of server shown by the frontend, or nil if it is not calculated (yet). It only
// looks at the cache, as calculating the matrix takes long.
func (d *SEOData) Matrix(server string) *SEOMatrix {
	key := server + constant.CacheSep + "true" + constant.CacheSep + SEOSourceCategory
	var result modelv2.DropMatrixQueryResult
	if err := cache.ShimGlobalDropMatrix.Get(key, &result); err != nil {
		return nil
	}
	var lastModified time.Time
	_ = cache.LastModifiedTime.Get("[shimGlobalDropMatrix#server|showClosedZones|sourceCategory:"+key+"]", &lastModified)

	d.matricesMu.Lock()
	memo, ok := d.matrices[server]
	if !ok {
		memo = &seoMatrixMemo{}
		d.matrices[server] = memo
	}
	d.matricesMu.Unlock()
	return memo.get(server, result.Matrix, lastModified)
}

// seoMatrixMemo keeps the SEOMatrix of a server built from the cached matrix, until the cached matrix is replaced
type seoMatrixMemo struct {
	mu     sync.Mutex
	first  *modelv2.OneDropMatrixElement
	n      int
	at     time.Time
	matrix *SEOMatrix
}

func (m *seoMatrixMemo) get(server string, rows []*modelv2.OneDropMatrixElement, lastModified time.Time) *SEOMatrix {
	var first *modelv2.OneDropMatrixElement
	if len(rows) > 0 {
		first = rows[0]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.matrix == nil || m.first != first || m.n != len(rows) || !m.at.Equal(lastModified) {
		m.first, m.n, m.at = first, len(rows), lastModified
		m.matrix = NewSEOMatrix(server, rows, lastModified)
	}
	return m.matrix
}

// SEOI18n is a text in the languages of the frontend (zh, en, ja, ko).
type SEOI18n map[string]string

// In returns the text in lang, or else in en, or else in zh, like the frontend (src/utils/strings.js translate); ""
// if there is none.
func (s SEOI18n) In(lang string) string {
	for _, l := range []string{lang, "en", "zh"} {
		if v := s[l]; v != "" {
			return v
		}
	}
	return ""
}

// SEOExistence is the existence of an item, stage or zone in each server.
type SEOExistence map[string]struct {
	Exist    bool     `json:"exist"`
	OpenTime null.Int `json:"openTime"`
}

func (e SEOExistence) In(server string) bool {
	return e[server].Exist
}

type SEOItem struct {
	ArkID     string
	Type      string
	Name      SEOI18n
	SortID    int
	Group     string
	Existence SEOExistence
}

type SEOZone struct {
	ArkID     string
	ZoneID    int
	Index     int
	Category  string
	Name      SEOI18n
	Existence SEOExistence
}

type SEOStage struct {
	ArkID     string
	StageID   int
	Code      SEOI18n
	Sanity    null.Int
	Zone      *SEOZone
	Existence SEOExistence
}

// SEOCatalog holds the items, stages and zones.
type SEOCatalog struct {
	Items map[string]*SEOItem
	// ItemList is ordered by sort ID
	ItemList []*SEOItem
	Stages   map[string]*SEOStage
	// StageList is ordered by the numerical stage ID
	StageList []*SEOStage
	Zones     map[string]*SEOZone
}

func NewSEOCatalog(items []*model.Item, stages []*model.Stage, zones []*model.Zone) *SEOCatalog {
	c := &SEOCatalog{
		Items:  make(map[string]*SEOItem, len(items)),
		Stages: make(map[string]*SEOStage, len(stages)),
		Zones:  make(map[string]*SEOZone, len(zones)),
	}
	zonesByID := make(map[int]*SEOZone, len(zones))
	for _, z := range zones {
		zone := &SEOZone{ArkID: z.ArkZoneID, ZoneID: z.ZoneID, Index: z.Index, Category: z.Category}
		unmarshalLenient(z.Name, &zone.Name)
		unmarshalLenient(z.Existence, &zone.Existence)
		c.Zones[zone.ArkID] = zone
		zonesByID[zone.ZoneID] = zone
	}
	for _, it := range items {
		item := &SEOItem{ArkID: it.ArkItemID, Type: it.Type, SortID: it.SortID, Group: it.Group.String}
		unmarshalLenient(it.Name, &item.Name)
		unmarshalLenient(it.Existence, &item.Existence)
		c.Items[item.ArkID] = item
		c.ItemList = append(c.ItemList, item)
	}
	for _, st := range stages {
		stage := &SEOStage{ArkID: st.ArkStageID, StageID: st.StageID, Sanity: st.Sanity, Zone: zonesByID[st.ZoneID]}
		unmarshalLenient(st.Code, &stage.Code)
		unmarshalLenient(st.Existence, &stage.Existence)
		c.Stages[stage.ArkID] = stage
		c.StageList = append(c.StageList, stage)
	}
	sort.SliceStable(c.ItemList, func(i, j int) bool { return c.ItemList[i].SortID < c.ItemList[j].SortID })
	sort.SliceStable(c.StageList, func(i, j int) bool { return c.StageList[i].StageID < c.StageList[j].StageID })
	return c
}

// unmarshalLenient leaves dest as is if raw is not what dest is: a malformed name or existence of one row must not
// keep the others from being shown
func unmarshalLenient(raw json.RawMessage, dest any) {
	if len(raw) == 0 {
		return
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		log.Warn().
			Str("evt.name", "seo.catalog.malformed").
			Err(err).
			Str("raw", string(raw)).
			Msg("malformed i18n text or existence")
	}
}

// SEORow is a row of the matrix with its drop rate.
type SEORow struct {
	*modelv2.OneDropMatrixElement
	// Rate is quantity / times rounded to 5 decimals, the frontend's percentage (src/utils/getters.js): the expected
	// quantity per run, which exceeds 1 for items dropped more than once per run on average
	Rate float64
}

// SEOMatrix is the global matrix of a server indexed by item and by stage.
type SEOMatrix struct {
	Server       string
	LastModified time.Time
	// ByItem holds the rows of each item ordered by rate, then times descending, then stage ID: the frontend's order
	ByItem map[string][]SEORow
	// ByStage holds the rows of each stage ordered by rate, then times descending, then item ID
	ByStage map[string][]SEORow
}

func NewSEOMatrix(server string, rows []*modelv2.OneDropMatrixElement, lastModified time.Time) *SEOMatrix {
	m := &SEOMatrix{
		Server:       server,
		LastModified: lastModified,
		ByItem:       make(map[string][]SEORow),
		ByStage:      make(map[string][]SEORow),
	}
	for _, el := range rows {
		row := SEORow{OneDropMatrixElement: el}
		if el.Times > 0 {
			row.Rate = math.Round(float64(el.Quantity)/float64(el.Times)*1e5) / 1e5
		}
		m.ByItem[el.ItemID] = append(m.ByItem[el.ItemID], row)
		m.ByStage[el.StageID] = append(m.ByStage[el.StageID], row)
	}
	for _, rows := range m.ByItem {
		sortSEORows(rows, func(r SEORow) string { return r.StageID })
	}
	for _, rows := range m.ByStage {
		sortSEORows(rows, func(r SEORow) string { return r.ItemID })
	}
	return m
}

func sortSEORows(rows []SEORow, id func(SEORow) string) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Rate != b.Rate {
			return a.Rate > b.Rate
		}
		if a.Times != b.Times {
			return a.Times > b.Times
		}
		return id(a) < id(b)
	})
}
