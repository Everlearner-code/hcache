package main

/*
 * Copyright 2014-2015 Albert P. Tobey <atobey@datastax.com> @AlTobey
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * pcstat.go - page cache stat
 *
 * uses the mincore(2) syscall to find out which pages (almost always 4k)
 * of a file are currently cached in memory
 *
 */

import (
	"flag"
	"fmt"
	"hcache/pkg/utils"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	pcstat "github.com/tobert/pcstat/pkg"
)

var (
	pidFlag, topFlag                            int
	terseFlag, nohdrFlag, jsonFlag, unicodeFlag bool
	plainFlag, ppsFlag, histoFlag, bnameFlag    bool
)

func init() {
	// TODO: error on useless/broken combinations
	flag.IntVar(&pidFlag, "pid", 0, "show all open maps for the given pid")
	flag.IntVar(&topFlag, "top", 0, "show top x cached files in descending order")
	flag.BoolVar(&terseFlag, "terse", false, "show terse output")
	flag.BoolVar(&nohdrFlag, "nohdr", false, "omit the header from terse & text output")
	flag.BoolVar(&jsonFlag, "json", false, "return data in JSON format")
	flag.BoolVar(&unicodeFlag, "unicode", false, "return data with unicode box characters")
	flag.BoolVar(&plainFlag, "plain", false, "return data with no box characters")
	flag.BoolVar(&ppsFlag, "pps", false, "include the per-page status in JSON output")
	flag.BoolVar(&histoFlag, "histo", false, "print a simple histogram instead of raw data")
	flag.BoolVar(&bnameFlag, "bname", false, "convert paths to basename to narrow the output")
}

func uniqueSlice(slice *[]string) {
	found := make(map[string]bool)
	total := 0
	for i, val := range *slice {
		if _, ok := found[val]; !ok {
			found[val] = true
			(*slice)[total] = (*slice)[i]
			total++
		}
	}

	*slice = (*slice)[:total]
}

func getStatsFromFiles(files []string) PcStatusList {

	stats := make(PcStatusList, 0, len(files))
	for _, fname := range files {
		status, err := pcstat.GetPcStatus(fname)
		if err != nil {
			log.Printf("skipping %q: %v", fname, err)
			continue
		}

		// convert long paths to their basename with the -bname flag
		// this overwrites the original filename in pcs but it doesn't matter since
		// it's not used to access the file again -- and should not be!
		if bnameFlag {
			status.Name = path.Base(fname)
		}

		stats = append(stats, status)
	}
	return stats
}

func formatStats(stats PcStatusList) {
	if jsonFlag {
		stats.FormatJson(!ppsFlag)
	} else if terseFlag {
		stats.FormatTerse()
	} else if histoFlag {
		stats.FormatHistogram()
	} else if unicodeFlag {
		stats.FormatUnicode()
	} else if plainFlag {
		stats.FormatPlain()
	} else {
		stats.FormatText()
	}
}

func top(top int) {
	p, err := utils.Processes()
	if err != nil {
		log.Fatalf("err: %s", err)
	}

	if len(p) <= 0 {
		log.Fatal("Cannot find any process.")
	}

	results := make([]utils.Process, 0, 50)

	for _, p1 := range p {
		if p1.RSS() != 0 {
			results = append(results, p1)
		}
	}

	var files []string

	for _, process := range results {
		pcstat.SwitchMountNs(process.Pid())
		maps := getPidLds(process.Pid())
		files = append(files, maps...)
	}

	uniqueSlice(&files)

	stats := getStatsFromFiles(files)

	sort.Sort(PcStatusList(stats))
	// TODO 修正切片长度小于 top 的时候的报错
	topStats := stats[:top]
	formatStats(topStats)
}

func main() {
	flag.Parse()

	if topFlag != 0 {
		top(topFlag)
		os.Exit(0)
	}

	files := flag.Args()
	if pidFlag != 0 {
		pcstat.SwitchMountNs(pidFlag)
		maps := getPidLds(pidFlag)
		files = append(files, maps...)
	}

	// all non-flag arguments are considered to be filenames
	// this works well with shell globbing
	// file order is preserved throughout this program
	if len(files) == 0 {
		flag.Usage()
		os.Exit(1)
	}

	stats := getStatsFromFiles(files)
	sort.Sort(PcStatusList(stats))
	formatStats(stats)
}

func getPidLds(pid int) []string {
	// ignore the process of hcache itself
	if pid == os.Getpid() {
		return []string{}
	}

	dirname := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(dirname)
	if err != nil {
		if !strings.Contains(err.Error(), "no such file or directory") {
			log.Fatalf("could not open dir '%s': %v", dirname, err)
		}
		log.Printf("skipping %s: %v", dirname, err)
		return []string{}
	}

	// use a map to help avoid duplicates
	maps := make(map[string]bool)

	for _, entry := range entries {
		if !entry.IsDir() {
			symlink := fmt.Sprintf("/proc/%d/fd/%s", pid, entry.Name())
			fi, err := os.Lstat(symlink)
			if err != nil {
				log.Printf("could not open '%s' for read: %v", symlink, err)
				continue
			}
			// judge whether the file is a symlink, here, the result is true if the file is a symlink
			if fi.Mode()&os.ModeSymlink != 0 {
				target, err := filepath.EvalSymlinks(symlink)
				if err != nil {
					// ignore file not found error because this is quite common
					if !strings.Contains(err.Error(), "no such file or directory") {
						log.Printf("could not inspect symlink '%s': %v", symlink, err)
					}
					continue
				}
				if strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "/dev") && !strings.HasPrefix(target, "/proc") {
					maps[target] = true
				}
			}
		}
	}

	// convert back to a list
	out := make([]string, 0, len(maps))
	for key := range maps {
		out = append(out, key)
	}

	return out
}
