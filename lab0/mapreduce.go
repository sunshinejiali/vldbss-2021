package main

import (
	"bufio"
	"encoding/json"
	"hash/fnv"
	"io/ioutil"
	"log"
	"os"
	"path"
	"runtime"
	"strconv"
	"sync"
)

// KeyValue is a type used to hold the key/value pairs passed to the map and reduce functions.
type KeyValue struct {
	Key   string
	Value string
}

// ReduceF function from MIT 6.824 LAB1
type ReduceF func(key string, values []string) string

// MapF function from MIT 6.824 LAB1
type MapF func(filename string, contents string) []KeyValue

// jobPhase indicates whether a task is scheduled as a map or reduce task.
type jobPhase string

const (
	mapPhase    jobPhase = "mapPhase"
	reducePhase          = "reducePhase"
)

type task struct {
	dataDir    string
	jobName    string
	mapFile    string   // only for map, the input file
	phase      jobPhase // are we in mapPhase or reducePhase?
	taskNumber int      // this task's index in the current phase
	nMap       int      // number of map tasks
	nReduce    int      // number of reduce tasks
	mapF       MapF     // map function used in this job
	reduceF    ReduceF  // reduce function used in this job
	wg         sync.WaitGroup
}

// MRCluster represents a map-reduce cluster.
type MRCluster struct {
	nWorkers int
	wg       sync.WaitGroup
	taskCh   chan *task
	exit     chan struct{}
}

var singleton = &MRCluster{
	nWorkers: runtime.NumCPU(),
	taskCh:   make(chan *task),
	exit:     make(chan struct{}),
}

func init() {
	singleton.Start()
}

// GetMRCluster returns a reference to a MRCluster.
func GetMRCluster() *MRCluster {
	return singleton
}

// NWorkers returns how many workers there are in this cluster.
func (c *MRCluster) NWorkers() int { return c.nWorkers }

// Start starts this cluster.
func (c *MRCluster) Start() {
	for i := 0; i < c.nWorkers; i++ {
		c.wg.Add(1)
		go c.worker()
	}
}

func (c *MRCluster) worker() {
	defer c.wg.Done()
	for {
		select {
		case t := <-c.taskCh:
			if t.phase == mapPhase {
				content, err := ioutil.ReadFile(t.mapFile)
				if err != nil {
					panic(err)
				}
				fs := make([]*os.File, t.nReduce)
				bs := make([]*bufio.Writer, t.nReduce)
				for i := range fs {
					rpath := reduceName(t.dataDir, t.jobName, t.taskNumber, i)
					fs[i], bs[i] = CreateFileAndBuf(rpath)
				}
				results := t.mapF(t.mapFile, string(content))
				for _, kv := range results {
					enc := json.NewEncoder(bs[ihash(kv.Key)%t.nReduce])
					if err := enc.Encode(&kv); err != nil {
						log.Fatalln(err)
					}
				}
				for i := range fs {
					SafeClose(fs[i], bs[i])
				}
			} else {
				// YOUR CODE HERE :)
				// hint: don't encode results returned by ReduceF, and just output
				// them into the destination file directly so that users can get
				// results formatted as what they want.
				// panic("YOUR CODE HERE")
				// save result[key]:[value][value]...
				result :=  make(map[string][]string)
				// nMap tasks
				for i := 0; i < t.nMap; i++ {
					rPath := reduceName(t.dataDir, t.jobName, i, t.taskNumber)
					// file -> *os.File
					file, err := os.Open(rPath)
					if err != nil {
						panic(err)
					}
					// decode
					dec := json.NewDecoder(file)
					var kv KeyValue
					for {
						if dec.Decode(&kv) != nil {
							break
						}
						if result[kv.Key] == nil {
							result[kv.Key] = make([]string, 0)
							result[kv.Key] = append(result[kv.Key], kv.Value)
						} else {
							result[kv.Key] = append(result[kv.Key], kv.Value)
						}
					}
					// close file
					SafeClose(file, nil)
				}
				// write to the file
				rPath := mergeName(t.dataDir, t.jobName, t.taskNumber)
				fs, bs := CreateFileAndBuf(rPath)
				for key, kvs := range result {
					// call reduceF, get the string 's'
					s := t.reduceF(key, kvs)
					WriteToBuf(bs, s)
				}
				SafeClose(fs, bs)
			}
			t.wg.Done()
		case <-c.exit:
			return
		}
	}
}

// Shutdown shutdowns this cluster.
func (c *MRCluster) Shutdown() {
	close(c.exit)
	c.wg.Wait()
}

// Submit submits a job to this cluster.
func (c *MRCluster) Submit(jobName, dataDir string, mapF MapF, reduceF ReduceF, mapFiles []string, nReduce int) <-chan []string {
	notify := make(chan []string)
	go c.run(jobName, dataDir, mapF, reduceF, mapFiles, nReduce, notify)
	return notify
}

func (c *MRCluster) run(jobName, dataDir string, mapF MapF, reduceF ReduceF, mapFiles []string, nReduce int, notify chan<- []string) {
	// map phase
	nMap := len(mapFiles)
	tasks := make([]*task, 0, nMap)
	for i := 0; i < nMap; i++ {
		t := &task{
			dataDir:    dataDir,
			jobName:    jobName,
			mapFile:    mapFiles[i],
			phase:      mapPhase,
			taskNumber: i,
			nReduce:    nReduce,
			nMap:       nMap,
			mapF:       mapF,
		}
		t.wg.Add(1)
		tasks = append(tasks, t)
		go func() { c.taskCh <- t }()
	}
	for _, t := range tasks {
		t.wg.Wait()
	}

	// reduce phase
	// YOUR CODE HERE :D
	// panic("YOUR CODE HERE")
	tasks = make([]*task, 0, nReduce)
	for i := 0; i < nReduce; i++ {
		t := &task{
			dataDir:    dataDir,
			jobName:    jobName,
			phase:      reducePhase,
			taskNumber: i,
			nReduce:    nReduce,
			nMap:       nMap,
			reduceF:    reduceF,
		}
		t.wg.Add(1)
		tasks = append(tasks, t)
		go func() { c.taskCh <- t }()
	}
	for _, t := range tasks {
		t.wg.Wait()
	}
	// result : output(contain all files)
	outPut := make([]string, 0)
	for i := 0; i < nReduce; i++ {
		rPath := mergeName(dataDir, jobName, i)
		outPut = append(outPut, rPath)
	}
	// chan
	notify <- outPut
}

func ihash(s string) int {
	h := fnv.New32a()
	h.Write([]byte(s))
	return int(h.Sum32() & 0x7fffffff)
}

func reduceName(dataDir, jobName string, mapTask int, reduceTask int) string {
	return path.Join(dataDir, "mrtmp."+jobName+"-"+strconv.Itoa(mapTask)+"-"+strconv.Itoa(reduceTask))
}

func mergeName(dataDir, jobName string, reduceTask int) string {
	return path.Join(dataDir, "mrtmp."+jobName+"-res-"+strconv.Itoa(reduceTask))
}
