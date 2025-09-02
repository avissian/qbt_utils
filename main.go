package main

import (
	"errors"
	"fmt"
	"github.com/alecthomas/kingpin/v2"
	"github.com/avissian/banner/tlo_config"
	"github.com/avissian/go-qbittorrent/qbt"
	"github.com/davecgh/go-spew/spew"
	"github.com/pterm/pterm"
	"github.com/ungerik/go-dry"
	"golang.org/x/exp/maps"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	kB = 1024
	mB = kB * 1024
	gB = mB * 1024
)

func Connect(user string, pass string, server string, port uint32, SSL bool, silent bool) (client *qbt.Client) {
	scheme := "http"
	if SSL {
		scheme = "https"
	}
	lo := qbt.LoginOptions{Username: user, Password: pass}
	client = qbt.NewClient(fmt.Sprintf("%s://%s:%d", scheme, server, port))
	if err := client.Login(lo); err != nil {
		log.Println(err)
		return
	}
	if !silent {
		buildInfo, err := client.BuildInfo()
		if err != nil {
			log.Println(err)
		}
		version, _ := client.ApplicationVersion()
		log.Printf("Connected (%s:%d): %#v %s", server, port, buildInfo, version)
	}
	return
}

func addPath(paths *[]string, path string) {
	re := regexp.MustCompile("^\\d+$")
	pathElem := strings.Split(path, string(os.PathSeparator))
	pathTmp := ""
	for _, v := range pathElem {
		if re.MatchString(v) {
			break
		}
		pathTmp += v + string(os.PathSeparator)
	}
	for _, v := range *paths {
		if v == pathTmp {
			return
		}
	}
	*paths = append(*paths, pathTmp)
}

func catInfo(clients *[]*qbt.Client, wg *sync.WaitGroup) {
	defer wg.Done()
	type catS struct {
		Size  uint64
		Count int
		Paths []string
	}

	cats := make(map[string]catS)

	for _, client := range *clients {
		s := "all" //"downloading" //"completed"
		tl, _ := client.Torrents(qbt.TorrentsOptions{Filter: &s})

		for _, t := range tl {
			paths := cats[t.Category].Paths
			addPath(&paths, t.SavePath)
			cats[t.Category] = catS{cats[t.Category].Size + uint64(t.TotalSize), cats[t.Category].Count + 1, paths}
			if t.State == "missingFiles" {
				log.Printf("missingFiles: %s \"%s\":  %s", client.URL, t.Name, t.SavePath)
			}
		}
	}
	///
	type sortS struct {
		sort uint64
		name string
	}
	sortArr := make([]sortS, len(cats))
	idx := 0
	for key, val := range cats {
		sortArr[idx].name = key
		sortArr[idx].sort = val.Size
		idx++
	}
	sort.Slice(sortArr, func(i int, j int) bool { return sortArr[i].sort > sortArr[j].sort })

	data := pterm.TableData{
		{"Cat", "Size, Gb", "Count", "Paths"},
	}
	for _, v := range sortArr {
		sort.Slice(cats[v.name].Paths, func(i, j int) bool { return cats[v.name].Paths[i] < cats[v.name].Paths[j] })
		data = append(data, []string{
			v.name, // Cat
			fmt.Sprintf("%.2f", float64(cats[v.name].Size)/gB), // Size, Gb
			strconv.Itoa(cats[v.name].Count),                   // Count
			strings.Join(cats[v.name].Paths, "|"),              // Paths
		})
	}
	_ = pterm.DefaultTable.WithHasHeader().WithData(data).Render()
}

func findDoubles(clients *[]*qbt.Client, delete bool, wg *sync.WaitGroup) {
	defer wg.Done()
	var hashesForDelete []string
	re := regexp.MustCompile("rutracker.*=([0-9]+)$")
	ids := make(map[string][]qbt.TorrentInfo)
	for _, client := range *clients {
		s := "all"
		tl, err := client.Torrents(qbt.TorrentsOptions{Filter: &s})
		if err != nil {
			continue
		}
		for _, t := range tl {
			theme := ""
			ti, _ := client.Torrent(t.Hash)
			matches := re.FindAllStringSubmatch(ti.Comment, -1)
			if re.MatchString(ti.Comment) {
				theme = matches[0][1]
			}
			if theme != "" {
				ids[theme] = append(ids[theme], t)
			}
		}
	}

	for _, theme := range ids {
		if len(theme) <= 1 {
			continue
		}
		added := int64(^uint64(0) >> 1)
		idx := 0

		for k, v := range theme {
			if v.AddedOn < added {
				added = v.AddedOn
				idx = k
			}
		}
		if delete {
			hashesForDelete = append(hashesForDelete, theme[idx].Hash)
		} else {
			log.Printf("Duble: %s %s\n", theme[idx].Name, theme[idx].Hash)
		}
	}

	if len(hashesForDelete) > 0 {
		for _, client := range *clients {
			_ = client.Delete(hashesForDelete, false)
			log.Printf("Deleted: %#v\n", hashesForDelete)
		}
	}
}

func infoExtended(clients *[]*qbt.Client, wg *sync.WaitGroup) {
	defer wg.Done()
	type statS struct {
		TotalSize int64
		Count     int
	}

	stats := make(map[string]statS)

	for _, client := range *clients {
		filter := ""
		tl, _ := client.Torrents(qbt.TorrentsOptions{Filter: &filter})
		var hashes []string
		var hashes2 []string
		var hashes3 []string
		for _, t := range tl {
			hashes = append(hashes, t.Hash)
			forced := ""
			if t.ForceStart {
				forced = "+F"
			}
			stats[t.State+forced] = statS{
				stats[t.State].TotalSize + t.TotalSize,
				stats[t.State].Count + 1,
			}
			if t.State == "missingFiles" {
				log.Printf("missingFiles: %s \"%s\":  %s", client.URL, t.Name, t.SavePath)
			}
			if t.State == "checkingDL" {
				hashes3 = append(hashes3, t.Hash)
			}
			if t.ForceStart && strings.HasPrefix(t.State, "forced") {
				hashes2 = append(hashes2, t.Hash)
			}
		}
		client.SetForceStart(hashes2, false)
		client.SetForceStart(hashes3, true)
	}
	///
	sortArr := maps.Keys(stats)
	sort.Slice(sortArr, func(i, j int) bool { return sortArr[i] < sortArr[j] })

	data := pterm.TableData{{"Status", "TotalSize Gb", "Count"}}
	for _, v := range sortArr {
		data = append(data, []string{
			v,
			fmt.Sprintf("%.2f", float64(stats[v].TotalSize)/gB),
			strconv.Itoa(stats[v].Count),
		},
		)
	}
	_ = pterm.DefaultTable.WithHasHeader().WithData(data).Render()
}

func checkStatus(clients *[]*qbt.Client) {

	for _, client := range *clients {
		tl, _ := client.Torrents(qbt.TorrentsOptions{})
		for _, t := range tl {
			switch t.State {
			case "error":
				fallthrough
			case "missingFiles":
				fallthrough
			case "checkingUP":
				fallthrough
			case "allocating":
				fallthrough
			case "checkingDL":
				fallthrough
			case "checkingResumeData":
				fallthrough
			case "moving":
				fallthrough
			case "unknown":
				os.Exit(1)
			}
		}
	}
}

func loadBallance(clients *[]*qbt.Client, queueSize int, wg *sync.WaitGroup) {
	wg.Done()
	for _, client := range *clients {
		filter := "downloading"
		tl, err := client.Torrents(qbt.TorrentsOptions{Filter: &filter})
		dry.PanicIfErr(err)
		stalledCnt := 0
		for _, torrent := range tl {
			if torrent.State == "stalledDL" {
				stalledCnt++
			}
		}
		opts := map[string]any{
			"queueing_enabled":         true,
			"max_active_downloads":     stalledCnt + queueSize,
			"max_active_torrents":      -1,
			"dont_count_slow_torrents": false,
		}

		err = client.SetPreferences(opts)
		dry.PanicIfErr(err)

	}
}

func downloadFile(URL, fileName string) (err error) {
	//Get the response bytes from the url
	response, err := http.Get(URL)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != 200 {
		return errors.New("Received non 200 response code")
	}
	file, err := os.Create(fileName)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, err = io.Copy(file, response.Body)
	if err != nil {
		return err
	}
	return
}

func renewFilters(clients *[]*qbt.Client, wg *sync.WaitGroup) {
	defer wg.Done()
	fileList := make(map[string]interface{})
	for _, client := range *clients {
		prefs, err := client.Preferences()
		dry.PanicIfErr(err)
		fileList[prefs.IpFilterPath] = 0
	}
	for file := range fileList {
		err := downloadFile("https://bot.keeps.cyou/static/ipfilter.dat", file)
		dry.PanicIfErr(err)
	}
	for _, client := range *clients {
		err := client.SetPreferences(map[string]any{"ip_filter_enabled": false})
		dry.PanicIfErr(err)
		err = client.SetPreferences(map[string]any{"ip_filter_enabled": true})
		dry.PanicIfErr(err)
		err = client.SetPreferences(map[string]any{"banned_IPs": ""})
		dry.PanicIfErr(err)
	}
}

func showErrors(clients *[]*qbt.Client, wg *sync.WaitGroup) {
	defer wg.Done()

	wg2 := sync.WaitGroup{}
	for _, client := range *clients {
		wg2.Add(1)
		go func(client *qbt.Client) {
			defer wg2.Done()

			tl, _ := client.Torrents(qbt.TorrentsOptions{})
			for _, t := range tl {
				switch t.State {
				case "error":
					fallthrough
				case "missingFiles":

					log.Printf("%s\n", client.URL)
					spew.Config.Indent = "  "
					spew.Dump(t)
				}
			}
		}(client)
	}
	wg2.Wait()

}

func pauseAll(clients *[]*qbt.Client, pause bool, resume bool, wg *sync.WaitGroup) {
	defer wg.Done()
	wg2 := sync.WaitGroup{}
	for _, client := range *clients {
		wg2.Add(1)
		go func(client *qbt.Client) {
			defer wg2.Done()
			var hashes []string
			tl, _ := client.Torrents(qbt.TorrentsOptions{})
			for _, torrent := range tl {
				hashes = append(hashes, torrent.Hash)
			}
			if pause {
				_ = client.Pause(hashes)
			}
			if resume {
				err := client.Resume(hashes)
				if err != nil {
					fmt.Println(err)
				}
			}
			//fmt.Printf("%v %v", pause, hashes)
		}(client)
	}
	wg2.Wait()
}

func main() {
	log.SetOutput(os.Stdout)
	//
	configPath := kingpin.Arg("path", "Путь к файлу конфига ТЛО").Required().File()
	queueF := kingpin.Flag("queue", "Подтюнить очередь на закачку").Short('q').Int()
	loopF := kingpin.Flag("loop", "Зациклить выполнение, раз в 60 секунд").Short('l').Bool()
	pauseF := kingpin.Flag("pause", "Остановить всё").Short('p').Bool()
	resumeF := kingpin.Flag("resume", "Запустить всё").Short('r').Bool()
	filtersF := kingpin.Flag("filters", "Обновить IP Filters").Short('f').Bool()
	infoF := kingpin.Flag("info", "Инфа о статусах").Short('i').Bool()
	searchF := kingpin.Flag("search", "Поиск по forum_id").Short('s').String()
	searchByHashF := kingpin.Flag("search-hash", "Поиск по hash раздачи").String()
	catF := kingpin.Flag("categories", "Подробно по категориям").Short('c').Bool()
	doublesF := kingpin.Flag("doubles", "Поиск и удаление дублей по forum_id").Short('d').Bool()
	checkF := kingpin.Flag("check", "Проверка статусов раздач, которые не попадут в отчёты").Bool()
	errorsF := kingpin.Flag("errors", "Вывод ошибок").Short('e').Bool()
	colorF := kingpin.Flag("color", "Цвет в выводе").Bool()
	silentF := kingpin.Flag("silent", "Выводить меньше сообщений").Short('m').Bool()
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "--help")
	}
	kingpin.Parse()
	if !*silentF {
		printVersion()
	}
	if *pauseF && *resumeF {
		panic("Должен быть задан только один параметр, pause или resume")
	}
	//
	var tlo tlo_config.ConfigT
	err := tlo.Load((*configPath).Name())
	dry.PanicIfErr(err)
	clients := make([]*qbt.Client, len(tlo.Clients))
	for idx, clientCfg := range tlo.Clients {
		clients[idx] = Connect(clientCfg.Login, clientCfg.Pass, clientCfg.Host, clientCfg.Port, clientCfg.SSL, *silentF)
	}
	if !*colorF {
		pterm.DisableColor()
	}
	var wg sync.WaitGroup
	/**/
	for {
		if *queueF > 0 { // default 0
			wg.Add(1)
			go loadBallance(&clients, *queueF, &wg)
		}

		if *filtersF {
			wg.Add(1)
			go renewFilters(&clients, &wg)
		}
		if *catF {
			wg.Add(1)
			go catInfo(&clients, &wg)
		}
		if *doublesF {
			wg.Add(1)
			go findDoubles(&clients, true, &wg)
		}
		if *infoF {
			wg.Add(1)
			go infoExtended(&clients, &wg)
		}
		if *searchF != "" || *searchByHashF != "" {
			wg.Add(1)
			go findTorrent(&clients, *searchF, *searchByHashF, &wg)
		}
		if *pauseF || *resumeF {
			wg.Add(1)
			go pauseAll(&clients, *pauseF, *resumeF, &wg)
		}

		if *errorsF || *resumeF {
			wg.Add(1)
			go showErrors(&clients, &wg)
		}

		//
		if *loopF {
			time.Sleep(time.Second * 60)
			wg.Wait()
		} else {
			wg.Wait()
			break
		}
	}
	// выходит с кодом ошибки, должен быть в конце
	if *checkF {
		checkStatus(&clients)
	}

}
