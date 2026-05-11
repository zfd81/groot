package scheduler

import (
	"time"

	"github.com/go-co-op/gocron/v2"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// Scheduler wraps a gocron scheduler for unified job management
type Scheduler struct {
	gs  gocron.Scheduler
	log *logger.Logger
}

// New creates a new scheduler with the given location
func New(log *logger.Logger, maxConcurrent int) (*Scheduler, error) {
	opts := []gocron.SchedulerOption{
		gocron.WithLocation(time.Local),
	}
	if maxConcurrent > 0 {
		opts = append(opts, gocron.WithLimitConcurrentJobs(uint(maxConcurrent), gocron.LimitModeReschedule))
	}

	gs, err := gocron.NewScheduler(opts...)
	if err != nil {
		return nil, err
	}

	return &Scheduler{gs: gs, log: log}, nil
}

// AddCron adds a cron-scheduled job with tags
func (s *Scheduler) AddCron(cronExpr string, task gocron.Task, tags ...string) error {
	_, err := s.gs.NewJob(
		gocron.CronJob(cronExpr, false),
		task,
		append([]gocron.JobOption{}, gocron.WithTags(tags...))...,
	)
	if err != nil {
		s.log.Error("添加 cron job 失败", zap.String("cron", cronExpr), zap.Error(err))
		return err
	}
	return nil
}

// AddOnce adds a one-time job at the specified time
func (s *Scheduler) AddOnce(at time.Time, task gocron.Task, tags ...string) error {
	opts := []gocron.JobOption{gocron.WithTags(tags...)}
	_, err := s.gs.NewJob(
		gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(at)),
		task,
		opts...,
	)
	if err != nil {
		s.log.Error("添加 once job 失败", zap.Time("at", at), zap.Error(err))
		return err
	}
	return nil
}

// AddDuration adds a duration-based repeating job
func (s *Scheduler) AddDuration(interval time.Duration, task gocron.Task, tags ...string) error {
	opts := []gocron.JobOption{gocron.WithTags(tags...)}
	_, err := s.gs.NewJob(
		gocron.DurationJob(interval),
		task,
		opts...,
	)
	if err != nil {
		s.log.Error("添加 duration job 失败", zap.Duration("interval", interval), zap.Error(err))
		return err
	}
	return nil
}

// AddDaily adds a daily job at specified time
func (s *Scheduler) AddDaily(hour, minute int, task gocron.Task, tags ...string) error {
	opts := []gocron.JobOption{gocron.WithTags(tags...)}
	_, err := s.gs.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(
			gocron.NewAtTime(uint(hour), uint(minute), 0),
		)),
		task,
		opts...,
	)
	if err != nil {
		s.log.Error("添加 daily job 失败",
			zap.Int("hour", hour),
			zap.Int("minute", minute),
			zap.Error(err),
		)
		return err
	}
	return nil
}

// RemoveByTag removes all jobs matching the given tag
func (s *Scheduler) RemoveByTag(tag string) {
	s.gs.RemoveByTags(tag)
}

// Start begins scheduling jobs
func (s *Scheduler) Start() {
	s.gs.Start()
	s.log.Info("调度器已启动")
}

// Stop shuts down the scheduler gracefully
func (s *Scheduler) Stop() error {
	s.log.Info("调度器正在停止")
	return s.gs.Shutdown()
}
