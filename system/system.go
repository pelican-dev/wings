package system

import (
	"context"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/go-units"
	"net"
	"runtime"

	"github.com/acobaugh/osrelease"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/parsers/kernel"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
)

type Information struct {
	Version string            `json:"version"`
	Docker  DockerInformation `json:"docker"`
	System  System            `json:"system"`
}

type DockerInformation struct {
	Version    string           `json:"version"`
	Cgroups    DockerCgroups    `json:"cgroups"`
	Containers DockerContainers `json:"containers"`
	Storage    DockerStorage    `json:"storage"`
	Runc       DockerRunc       `json:"runc"`
}

type DockerCgroups struct {
	Driver  string `json:"driver"`
	Version string `json:"version"`
}

type DockerContainers struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Paused  int `json:"paused"`
	Stopped int `json:"stopped"`
}

type DockerStorage struct {
	Driver     string `json:"driver"`
	Filesystem string `json:"filesystem"`
}

type DockerRunc struct {
	Version string `json:"version"`
}

type System struct {
	Architecture  string `json:"architecture"`
	CPUThreads    int    `json:"cpu_threads"`
	MemoryBytes   int64  `json:"memory_bytes"`
	KernelVersion string `json:"kernel_version"`
	OS            string `json:"os"`
	OSType        string `json:"os_type"`
}

type IpAddresses struct {
	IpAddresses []string `json:"ip_addresses"`
}

type Utilization struct {
	MemoryTotal uint64  `json:"memory_total"`
	MemoryUsed  uint64  `json:"memory_used"`
	SwapTotal   uint64  `json:"swap_total"`
	SwapUsed    uint64  `json:"swap_used"`
	LoadAvg1    float64 `json:"load_average1"`
	LoadAvg5    float64 `json:"load_average5"`
	LoadAvg15   float64 `json:"load_average15"`
	CpuPercent  float64 `json:"cpu_percent"`
	DiskTotal   uint64  `json:"disk_total"`
	DiskUsed    uint64  `json:"disk_used"`
}

type DockerDiskUsage struct {
	ContainersSize string `json:"containers_size"`
	ImagesTotal    int    `json:"images_total"`
	ImagesActive   int64  `json:"images_active"`
	ImagesSize     string `json:"images_size"`
	BuildCacheSize int64  `json:"build_cache_size"`
}

func GetSystemInformation() (*Information, error) {
	k, err := kernel.GetKernelVersion()
	if err != nil {
		return nil, err
	}

	version, info, err := GetDockerInfo(context.Background())
	if err != nil {
		return nil, err
	}

	release, err := osrelease.Read()
	if err != nil {
		return nil, err
	}

	var os string
	if release["PRETTY_NAME"] != "" {
		os = release["PRETTY_NAME"]
	} else if release["NAME"] != "" {
		os = release["NAME"]
	} else {
		os = info.OperatingSystem
	}

	var filesystem string
	for _, v := range info.DriverStatus {
		if v[0] != "Backing Filesystem" {
			continue
		}
		filesystem = v[1]
		break
	}

	return &Information{
		Version: Version,
		Docker: DockerInformation{
			Version: version.Version,
			Cgroups: DockerCgroups{
				Driver:  info.CgroupDriver,
				Version: info.CgroupVersion,
			},
			Containers: DockerContainers{
				Total:   info.Containers,
				Running: info.ContainersRunning,
				Paused:  info.ContainersPaused,
				Stopped: info.ContainersStopped,
			},
			Storage: DockerStorage{
				Driver:     info.Driver,
				Filesystem: filesystem,
			},
			Runc: DockerRunc{
				Version: info.RuncCommit.ID,
			},
		},
		System: System{
			Architecture:  runtime.GOARCH,
			CPUThreads:    runtime.NumCPU(),
			MemoryBytes:   info.MemTotal,
			KernelVersion: k.String(),
			OS:            os,
			OSType:        runtime.GOOS,
		},
	}, nil
}

func GetSystemIps() (*IpAddresses, error) {
	var ip_addrs []string
	iface_addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range iface_addrs {
		ipNet, valid := addr.(*net.IPNet)
		if valid && !ipNet.IP.IsLoopback() {
			ip_addrs = append(ip_addrs, ipNet.IP.String())
		}
	}
	return &IpAddresses{IpAddresses: ip_addrs}, nil
}

func GetSystemUtilization() (*Utilization, error) {
	c, err := cpu.Percent(0, false)
	if err != nil {
		return nil, err
	}
	m, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}
	s, err := mem.SwapMemory()
	if err != nil {
		return nil, err
	}
	l, err := load.Avg()
	if err != nil {
		return nil, err
	}
	d, err := disk.Usage("/")
	if err != nil {
		return nil, err
	}

	return &Utilization{
		MemoryTotal: m.Total,
		MemoryUsed:  m.Used,
		SwapTotal:   s.Total,
		SwapUsed:    s.Used,
		CpuPercent:  c[0],
		LoadAvg1:    l.Load1,
		LoadAvg5:    l.Load5,
		LoadAvg15:   l.Load15,
		DiskTotal:   d.Total,
		DiskUsed:    d.Used,
	}, nil
}

func GetDockerDiskUsage(ctx context.Context) (*DockerDiskUsage, error) {
	// TODO: find a way to re-use the client from the docker environment.
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return &DockerDiskUsage{}, err
	}
	defer c.Close()

	d, err := c.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return &DockerDiskUsage{}, err
	}

	var bcs int64
	for _, bc := range d.BuildCache {
		if !bc.Shared {
			bcs += bc.Size
		}
	}

	var a int64
	for _, i := range d.Images {
		if i.Containers > 0 {
			a++
		}
	}

	var cs int64
	for _, b := range d.Containers {
		cs += b.SizeRootFs
	}

	return &DockerDiskUsage{
		ImagesTotal:    len(d.Images),
		ImagesActive:   a,
		ImagesSize:     units.HumanSize(float64(d.LayersSize)),
		ContainersSize: units.HumanSize(float64(cs)),
		BuildCacheSize: bcs,
	}, nil
}

func PruneDockerImages(ctx context.Context) (types.ImagesPruneReport, error) {
	// TODO: find a way to re-use the client from the docker environment.
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return types.ImagesPruneReport{}, err
	}
	defer c.Close()

	prune, err := c.ImagesPrune(ctx, filters.Args{})
	if err != nil {
		return types.ImagesPruneReport{}, err
	}
	return prune, nil
}

func GetDockerInfo(ctx context.Context) (types.Version, system.Info, error) {
	// TODO: find a way to re-use the client from the docker environment.
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return types.Version{}, system.Info{}, err
	}
	defer c.Close()

	dockerVersion, err := c.ServerVersion(ctx)
	if err != nil {
		return types.Version{}, system.Info{}, err
	}

	dockerInfo, err := c.Info(ctx)
	if err != nil {
		return types.Version{}, system.Info{}, err
	}

	return dockerVersion, dockerInfo, nil
}
