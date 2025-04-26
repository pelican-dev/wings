package system

import (
	"context"
	"net"
	"runtime"
	"syscall"

	"github.com/acobaugh/osrelease"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/parsers/kernel"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
)

/* ------------------------------------------------------------------ */
/*  Types                                                             */
/* ------------------------------------------------------------------ */

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

type DiskInfo struct {
	Device     string   `json:"device"`
	Mountpoint string   `json:"mountpoint"`
	TotalSpace uint64   `json:"total_space"`
	UsedSpace  uint64   `json:"used_space"`
	Tags       []string `json:"tags"`
}

type Utilization struct {
	MemoryTotal uint64     `json:"memory_total"`
	MemoryUsed  uint64     `json:"memory_used"`
	SwapTotal   uint64     `json:"swap_total"`
	SwapUsed    uint64     `json:"swap_used"`
	LoadAvg1    float64    `json:"load_average1"`
	LoadAvg5    float64    `json:"load_average5"`
	LoadAvg15   float64    `json:"load_average15"`
	CpuPercent  float64    `json:"cpu_percent"`
	DiskTotal   uint64     `json:"disk_total"`
	DiskUsed    uint64     `json:"disk_used"`
	DiskDetails []DiskInfo `json:"disk_details"`
}

type DockerDiskUsage struct {
	ContainersSize int64 `json:"containers_size"`
	ImagesTotal    int   `json:"images_total"`
	ImagesActive   int64 `json:"images_active"`
	ImagesSize     int64 `json:"images_size"`
	BuildCacheSize int64 `json:"build_cache_size"`
}

/* ------------------------------------------------------------------ */
/*  High-level information helpers                                    */
/* ------------------------------------------------------------------ */

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
		if v[0] == "Backing Filesystem" {
			filesystem = v[1]
			break
		}
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
	var ipAddrs []string
	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range ifaceAddrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			ipAddrs = append(ipAddrs, ipnet.IP.String())
		}
	}
	return &IpAddresses{IpAddresses: ipAddrs}, nil
}

/* ------------------------------------------------------------------ */
/*  Disk helpers                                                      */
/* ------------------------------------------------------------------ */

// getDiskForPath returns the device and mountpoint for a given path.
func getDiskForPath(path string, parts []disk.PartitionStat) (string, string, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "", "", err
	}
	for _, p := range parts {
		var pst syscall.Statfs_t
		if err := syscall.Statfs(p.Mountpoint, &pst); err != nil {
			continue
		}
		if st.Fsid == pst.Fsid {
			return p.Device, p.Mountpoint, nil
		}
	}
	return "", "", nil
}

/* ------------------------------------------------------------------ */
/*  Main Utilization collector                                        */
/* ------------------------------------------------------------------ */

func GetSystemUtilization(root, logs, data, archive, backup, temp string) (*Utilization, error) {
	// CPU, memory, load averages
	c, err := cpu.Percent(0, false)
	if err != nil {
		return nil, err
	}
	vmstat, _ := mem.VirtualMemory()
	swap, _ := mem.SwapMemory()
	loadavg, _ := load.Avg()

	// Path tags for DiskDetails
	paths := map[string]string{
		"Root": root, "Logs": logs, "Data": data,
		"Archive": archive, "Backup": backup, "Temp": temp,
	}

	partitions, err := disk.Partitions(false) // physical filesystems only
	if err != nil {
		return nil, err
	}

	/* ----------------------------------------------------------------
	   Disk usage – count each block-device exactly once.

	   gopsutil/disk.Partitions(false) may list multiple mount-points
	   for the same backing device (e.g. Docker overlay bind mounts).
	   Summing those individually inflates DiskTotal/DiskUsed.

	   We record the first sighting of every partition.Device and skip
	   duplicates so each physical disk is counted exactly one time.
	------------------------------------------------------------------ */

	seenDevices := make(map[string]struct{}) // device path → seen
	diskMap     := make(map[string]*DiskInfo)

	var diskTotal uint64
	var diskUsed  uint64

	for _, part := range partitions {
		if _, dup := seenDevices[part.Device]; dup {
			continue // already counted this block device
		}
		seenDevices[part.Device] = struct{}{}

		if u, err := disk.Usage(part.Mountpoint); err == nil {
			diskTotal += u.Total
			diskUsed += u.Used

			diskMap[part.Mountpoint] = &DiskInfo{
				Device:     part.Device,
				Mountpoint: part.Mountpoint,
				TotalSpace: u.Total,
				UsedSpace:  u.Used,
				Tags:       []string{},
			}
		}
	}
	/* ----------------------- end dedupe block ----------------------- */

	// Tag disks (root, logs, data, etc.)
	for tag, path := range paths {
		_, mp, err := getDiskForPath(path, partitions)
		if err == nil && mp != "" {
			if d, ok := diskMap[mp]; ok {
				d.Tags = append(d.Tags, tag)
			}
		}
	}

	details := make([]DiskInfo, 0, len(diskMap))
	for _, d := range diskMap {
		details = append(details, *d)
	}

	return &Utilization{
		MemoryTotal: vmstat.Total,
		MemoryUsed:  vmstat.Used,
		SwapTotal:   swap.Total,
		SwapUsed:    swap.Used,
		CpuPercent:  c[0],
		LoadAvg1:    loadavg.Load1,
		LoadAvg5:    loadavg.Load5,
		LoadAvg15:   loadavg.Load15,
		DiskTotal:   diskTotal,
		DiskUsed:    diskUsed,
		DiskDetails: details,
	}, nil
}

/* ------------------------------------------------------------------ */
/*  Docker helpers                                                    */
/* ------------------------------------------------------------------ */

func GetDockerDiskUsage(ctx context.Context) (*DockerDiskUsage, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return &DockerDiskUsage{}, err
	}
	defer cli.Close()

	d, err := cli.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return &DockerDiskUsage{}, err
	}

	var buildSize, containerSize int64
	for _, bc := range d.BuildCache {
		if !bc.Shared {
			buildSize += bc.Size
		}
	}
	for _, c := range d.Containers {
		containerSize += c.SizeRootFs
	}

	var activeImages int64
	for _, i := range d.Images {
		if i.Containers > 0 {
			activeImages++
		}
	}

	return &DockerDiskUsage{
		ImagesTotal:    len(d.Images),
		ImagesActive:   activeImages,
		ImagesSize:     int64(d.LayersSize),
		ContainersSize: containerSize,
		BuildCacheSize: buildSize,
	}, nil
}

func PruneDockerImages(ctx context.Context) (image.PruneReport, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return image.PruneReport{}, err
	}
	defer cli.Close()

	return cli.ImagesPrune(ctx, filters.Args{})
}

func GetDockerInfo(ctx context.Context) (types.Version, system.Info, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return types.Version{}, system.Info{}, err
	}
	defer cli.Close()

	version, err := cli.ServerVersion(ctx)
	if err != nil {
		return types.Version{}, system.Info{}, err
	}
	info, err := cli.Info(ctx)
	if err != nil {
		return types.Version{}, system.Info{}, err
	}
	return version, info, nil
}
