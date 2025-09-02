package main

import (
	"github.com/avissian/go-qbittorrent/qbt"
	"github.com/davecgh/go-spew/spew"
	"log"
	"regexp"
	"strings"
	"sync"
)

type TI struct {
	Torrent *qbt.TorrentInfo
	Client  *qbt.Client
}

func Worker(jobs <-chan *TI, themeSearch string, hashSearch string, wg *sync.WaitGroup) {
	defer wg.Done()
	re := regexp.MustCompile("rutracker.*=([0-9]+)$")
	for ti := range jobs {
		forumTheme := ""
		if themeSearch != "" {
			torrentInfo, _ := ti.Client.Torrent(ti.Torrent.Hash)

			if re.MatchString(torrentInfo.Comment) {
				matches := re.FindAllStringSubmatch(torrentInfo.Comment, -1)
				forumTheme = matches[0][1]
			}
		}
		if (themeSearch != "" &&
			forumTheme == themeSearch) ||
			(hashSearch != "" &&
				hashSearch == strings.ToLower(ti.Torrent.Hash)) {
			log.Printf("%s\n", ti.Client.URL)
			spew.Config.Indent = "  "
			spew.Dump(ti.Torrent)
			//return
		}
	}
}

func findTorrent(clients *[]*qbt.Client, themeSearch string, hashSearch string, wg *sync.WaitGroup) {
	defer wg.Done()

	const numWorkers = 4

	hashSearch = strings.ToLower(hashSearch)
	var wgWorkers sync.WaitGroup
	var wgClients sync.WaitGroup

	var torrentList []TI

	var mu sync.Mutex

	for _, client := range *clients {
		wgClients.Add(1)
		go func(client *qbt.Client, ti *[]TI) {
			defer wgClients.Done()
			tl, err := client.Torrents(qbt.TorrentsOptions{})
			if err != nil {
				panic(err)
			}
			for idx := range tl {
				mu.Lock()
				*ti = append(*ti, TI{&tl[idx], client})
				mu.Unlock()
			}

		}(client, &torrentList)
	}
	wgClients.Wait()

	jobs := make(chan *TI, len(torrentList))
	// Запускаем воркеры
	for i := 0; i < numWorkers; i++ {
		wgWorkers.Add(1)
		go Worker(jobs, themeSearch, hashSearch, &wgWorkers)
	}

	for _, tl := range torrentList {
		jobs <- &TI{tl.Torrent, tl.Client}
	}
	close(jobs)
	wgWorkers.Wait()
}
