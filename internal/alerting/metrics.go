package alerting

import "github.com/prometheus/client_golang/prometheus"

// metrics groups the Prometheus collectors the alerting service maintains.
type metrics struct {
	alertsFired     prometheus.Counter
	alertsDelivered prometheus.Counter
	deliveryErrors  prometheus.Counter
	historyErrors   prometheus.Counter
	refreshErrors   prometheus.Counter
	recordsSkipped  prometheus.Counter
	pollErrors      prometheus.Counter
	commitErrors    prometheus.Counter
	rulesLoaded     prometheus.Gauge
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		alertsFired: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "alerting_alerts_fired_total",
			Help: "Total number of rule firings evaluated from the tick stream.",
		}),
		alertsDelivered: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "alerting_alerts_delivered_total",
			Help: "Total number of alert notifications delivered successfully.",
		}),
		deliveryErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "alerting_delivery_errors_total",
			Help: "Total number of alert notifications that failed to deliver.",
		}),
		historyErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "alerting_history_errors_total",
			Help: "Total number of failed alert-history writes.",
		}),
		refreshErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "alerting_rule_refresh_errors_total",
			Help: "Total number of failed rule reloads from the database.",
		}),
		recordsSkipped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "alerting_records_skipped_total",
			Help: "Total number of records skipped because they could not be decoded or evaluated.",
		}),
		pollErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "alerting_poll_errors_total",
			Help: "Total number of failed poll attempts against Kafka.",
		}),
		commitErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "alerting_commit_errors_total",
			Help: "Total number of failed offset commits.",
		}),
		rulesLoaded: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "alerting_rules_loaded",
			Help: "Number of enabled alert rules currently loaded.",
		}),
	}
	reg.MustRegister(
		m.alertsFired, m.alertsDelivered, m.deliveryErrors, m.historyErrors,
		m.refreshErrors, m.recordsSkipped, m.pollErrors, m.commitErrors, m.rulesLoaded,
	)
	return m
}
