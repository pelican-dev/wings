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

/* ----------------------------- types ------------------------------ */

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

type DockerCgroups struct{ Driver, Version string }
type DockerContainers struct{ Total, Running, Paused, Stopped int }
type DockerStorage struct{ Driver, Filesystem string }
type DockerRunc struct{ Version string }

type System struct {
	Architecture  string `json:"architecture"`
	CPUThreads    int    `json:"cpu_threads"`
	MemoryBytes   int64  `json:"memory_bytes"`
	KernelVersion string `json:"kernel_version"`
	OS            string `json:"os"`
	OSType        string `json:"os_type"`
}

type IpAddresses struct{ IpAddresses []string }

type DiskInfo struct {
	Device, Mountpoint       string
	TotalSpace, UsedSpace    uint64
	Tags                     []string
}

type Utilization struct {
	MemoryTotal, MemoryUsed uint64
	SwapTotal, SwapUsed     uint64
	LoadAvg1, LoadAvg5,
	LoadAvg15, CpuPercent   float64
	DiskTotal, DiskUsed     uint64
	DiskDetails             []DiskInfo
}

type DockerDiskUsage struct {
	ContainersSize, ImagesActive,
	ImagesSize, BuildCacheSize int64
	ImagesTotal                int
}

/* ------------------ high-level information helpers ---------------- */

func GetSystemInformation() (*Information, error) {
	k, err := kernel.GetKernelVersion()
	if err != nil {
		return nil, err
	}

	version, info, err := GetDockerInfo(context.Background())
	if err != nil {
		return nil, err
	}

	release, _ := osrelease.Read()
	osName := info.OperatingSystem
	if v := release["PRETTY_NAME"]; v != "" {
		osName = v
	} else if v := release["NAME"]; v != "" {
		osName = v
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
			Cgroups: DockerCgroups{Driver: info.CgroupDriver, Version: info.CgroupVersion},
			Containers: DockerContainers{
				Total: info.Containers, Running: info.ContainersRunning,
				Paused: info.ContainersPaused, Stopped: info.ContainersStopped,
			},
			Storage: DockerStorage{Driver: info.Driver, Filesystem: filesystem},
			Runc:    DockerRunc{Version: info.RuncCommit.ID},
		},
		System: System{
			Architecture: runtime.GOARCH, CPUThreads: runtime.NumCPU(),
			MemoryBytes: info.MemTotal, KernelVersion: k.String(),
			OS: osName, OSType: runtime.GOOS,
		},
	}, nil
}

func GetSystemIps() (*IpAddresses, error) {
	var out []string
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	for _, a := range ifaces {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() {
			out = append(out, ipn.IP.String())
		}
	}
	return &IpAddresses{IpAddresses: out}, nil
}

/* --------------------------- disk helpers ------------------------- */

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

/* -------------------- main utilization collector ------------------ */

func GetSystemUtilization(root, logs, data, archive, backup, temp string) (*Utilization, error) {
	cpuPct, _ := cpu.Percent(0, false)
	vm, _ := mem.VirtualMemory()
	swap, _ := mem.SwapMemory()
	loadavg, _ := load.Avg()

	paths := map[string]string{"Root": root, "Logs": logs, "Data": data,
		"Archive": archive, "Backup": backup, "Temp": temp}

	parts, err := disk.Partitions(false)
	if err != nil {
		return nil, err
	}

	/* -------- disk usage: dedupe by Device --------------------------
	   Skip duplicate partition.Device entries so each block device is
	   counted exactly once (prevents inflated totals from overlay/binds)
	------------------------------------------------------------------ */
	seen := make(map[string]struct{})
	diskMap := make(map[string]*DiskInfo)
	var diskTotal, diskUsed uint64

	for _, p := range parts {
		if _, dup := seen[p.Device]; dup {
			continue
		}
		seen[p.Device] = struct{}{}
		if u, err := disk.Usage(p.Mountpoint); err == nil {
			diskTotal += u.Total
			diskUsed += u.Used
			diskMap[p.Mountpoint] = &DiskInfo{
				Device:     p.Device,
				Mountpoint: p.Mountpoint,
				TotalSpace: u.Total,
				UsedSpace:  u.Used,
			}
		}
	}
	/* --------------------------------------------------------------- */

	// tag disks
	for tag, pth := range paths {
		_, mp, _ := getDiskForPath(pth, parts)
		if d, ok := diskMap[mp]; ok {
			d.Tags = append(d.Tags, tag)
		}
	}
	var details []DiskInfo
	for _, d := range diskMap {
		details = append(details, *d)
	}

	return &Utilization{
		MemoryTotal: vm.Total, MemoryUsed: vm.Used,
		SwapTotal: swap.Total, SwapUsed: swap.Used,
		CpuPercent: cpuPct[0],
		LoadAvg1: loadavg.Load1, LoadAvg5: loadavg.Load5, LoadAvg15: loadavg.Load15,
		DiskTotal: diskTotal, DiskUsed: diskUsed, DiskDetails: details,
	}, nil
}

/* --------------------------- docker helpers ----------------------- */

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
		ImagesTotal: len(d.Images), ImagesActive: activeImages,
		ImagesSize: int64(d.LayersSize),
		ContainersSize: containerSize, BuildCacheSize: buildSize,
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

	v, err := cli.ServerVersion(ctx)
	if err != nil {
		return types.Version{}, system.Info{}, err
	}
	i, err := cli.Info(ctx)
	if err != nil {
		return types.Version{}, system.Info{}, err
	}
	return v, i, nil
}
