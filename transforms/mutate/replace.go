package mutate

import (
	"fmt"
	"regexp"
	"sort"
	"sync"

	"github.com/qiniu/logkit/transforms"
	. "github.com/qiniu/logkit/utils/models"
)

var (
	_ transforms.StatsTransformer = &Replacer{}
	_ transforms.Transformer      = &Replacer{}
	_ transforms.Initializer      = &Replacer{}
)

type Replacer struct {
	StageTime string `json:"stage"`
	Key       string `json:"key"`
	Old       string `json:"old"`
	New       string `json:"new"`
	Regex     bool   `json:"regex"`
	stats     StatsInfo
	Regexp    *regexp.Regexp

	keys       []string
	numRoutine int
}

func (g *Replacer) Init() error {
	rgexpr := g.Old
	if !g.Regex {
		rgexpr = regexp.QuoteMeta(g.Old)
	}
	rgx, err := regexp.Compile(rgexpr)
	if err != nil {
		return err
	}
	g.Regexp = rgx
	g.keys = GetKeys(g.Key)
	numRoutine := MaxProcs
	if numRoutine == 0 {
		numRoutine = 1
	}
	g.numRoutine = numRoutine
	return nil
}

func (g *Replacer) Transform(datas []Data) ([]Data, error) {
	if len(g.keys) == 0 {
		g.Init()
	}
	var (
		err, fmtErr error
		errNum      int
	)

	numRoutine := g.numRoutine
	if len(datas) < numRoutine {
		numRoutine = len(datas)
	}
	dataPipline := make(chan transforms.TransformInfo)
	resultChan := make(chan transforms.TransformResult)

	wg := new(sync.WaitGroup)
	for i := 0; i < numRoutine; i++ {
		wg.Add(1)
		go g.transform(dataPipline, resultChan, wg)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	go func() {
		for idx, data := range datas {
			dataPipline <- transforms.TransformInfo{
				CurData: data,
				Index:   idx,
			}
		}
		close(dataPipline)
	}()

	var transformResultSlice = make(transforms.TransformResultSlice, 0, len(datas))
	for resultInfo := range resultChan {
		transformResultSlice = append(transformResultSlice, resultInfo)
	}
	if numRoutine > 1 {
		sort.Stable(transformResultSlice)
	}

	for _, transformResult := range transformResultSlice {
		if transformResult.Err != nil {
			err = transformResult.Err
			errNum += transformResult.ErrNum
		}
		datas[transformResult.Index] = transformResult.CurData
	}

	g.stats, fmtErr = transforms.SetStatsInfo(err, g.stats, int64(errNum), int64(len(datas)), g.Type())
	return datas, fmtErr
}

func (g *Replacer) RawTransform(datas []string) ([]string, error) {
	for i := range datas {
		datas[i] = g.Regexp.ReplaceAllString(datas[i], g.New)
	}

	g.stats, _ = transforms.SetStatsInfo(nil, g.stats, 0, int64(len(datas)), g.Type())
	return datas, nil
}

func (g *Replacer) Description() string {
	//return "replace old string to new"
	return "用新字符串替换旧字符串"
}

func (g *Replacer) Type() string {
	return "replace"
}

func (g *Replacer) SampleConfig() string {
	return `{
		"type":"replace",
		"stage":"before_parser",
		"key":"MyReplaceFieldKey",
		"old":"myOldString",
		"new":"myNewString",
        "regex":"false"
	}`
}

func (g *Replacer) ConfigOptions() []Option {
	return []Option{
		transforms.KeyStage,
		transforms.KeyFieldName,
		{
			KeyName:      "old",
			ChooseOnly:   false,
			Default:      "",
			Required:     true,
			Placeholder:  "myOldString",
			DefaultNoUse: true,
			Description:  "要替换的字符串内容(old)",
			Type:         transforms.TransformTypeString,
		},
		{
			KeyName:      "new",
			ChooseOnly:   false,
			Default:      "",
			Required:     false,
			Placeholder:  "myNewString",
			DefaultNoUse: true,
			Description:  "替换为的字符串内容(new)",
			Type:         transforms.TransformTypeString,
		},
		{
			KeyName:       "regex",
			Element:       Radio,
			ChooseOnly:    true,
			ChooseOptions: []interface{}{false, true},
			Default:       false,
			DefaultNoUse:  false,
			Description:   "是否启用正则匹配(regex)",
			Type:          transforms.TransformTypeBoolean,
		},
	}
}

func (g *Replacer) Stage() string {
	if g.StageTime == "" {
		return transforms.StageBeforeParser
	}
	return g.StageTime
}

func (g *Replacer) Stats() StatsInfo {
	return g.stats
}

func (g *Replacer) SetStats(err string) StatsInfo {
	g.stats.LastError = err
	return g.stats
}

func init() {
	transforms.Add("replace", func() transforms.Transformer {
		return &Replacer{}
	})
}

func (g *Replacer) transform(dataPipline <-chan transforms.TransformInfo, resultChan chan transforms.TransformResult, wg *sync.WaitGroup) {
	var (
		err    error
		errNum int
	)
	for transformInfo := range dataPipline {
		err = nil
		errNum = 0

		val, getErr := GetMapValue(transformInfo.CurData, g.keys...)
		if getErr != nil {
			errNum++
			err = fmt.Errorf("transform key %v not exist in data", g.Key)
			resultChan <- transforms.TransformResult{
				Index:   transformInfo.Index,
				CurData: transformInfo.CurData,
				Err:     err,
				ErrNum:  errNum,
			}
			continue
		}
		strVal, ok := val.(string)
		if !ok {
			errNum++
			err = fmt.Errorf("transform key %v data type is not string", g.Key)
			resultChan <- transforms.TransformResult{
				Index:   transformInfo.Index,
				CurData: transformInfo.CurData,
				Err:     err,
				ErrNum:  errNum,
			}
			continue
		}
		setErr := SetMapValue(transformInfo.CurData, g.Regexp.ReplaceAllString(strVal, g.New), false, g.keys...)
		if setErr != nil {
			errNum++
			err = fmt.Errorf("value of %v is not the type of map[string]interface{}", g.Key)
		}

		resultChan <- transforms.TransformResult{
			Index:   transformInfo.Index,
			CurData: transformInfo.CurData,
			Err:     err,
			ErrNum:  errNum,
		}
	}
	wg.Done()
}
