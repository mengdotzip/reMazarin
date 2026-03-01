package main

import (
	"context"
	"errors"
	"time"

	"github.com/mdobak/go-xerrors"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func setupOTelSDK(ctx context.Context, cfg *Config) (shutdown func(context.Context) error, err error) {
	config := cfg
	var shutdownFuncs []func(context.Context) error

	shutdown = func(ctx context.Context) error {
		var errs []error
		for _, fn := range shutdownFuncs {
			if e := fn(ctx); e != nil {
				errs = append(errs, e)
			}
		}
		if len(errs) > 0 {
			return xerrors.Newf("otel shutdown: %w", errors.Join(errs...))
		}
		return nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("remazarin"),
			semconv.ServiceVersion(version),
		),
	)
	if err != nil {
		return shutdown, xerrors.Newf("create otel resource: %w", err)
	}

	// Propagator
	// Enables trace context to flow through HTTP headers (W3C TraceContext + Baggage).
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Trace exporter
	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(config.Otel.Endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return shutdown, xerrors.Newf("create trace exporter: %w", err)
	}
	shutdownFuncs = append(shutdownFuncs, traceExporter.Shutdown)

	tracerProvider := trace.NewTracerProvider(
		trace.WithResource(res),
		trace.WithBatcher(traceExporter,
			trace.WithBatchTimeout(time.Duration(config.Otel.Interval)),
		),

		trace.WithSampler(trace.TraceIDRatioBased(0.1)),
	)
	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
	otel.SetTracerProvider(tracerProvider)

	// Metric exporter
	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(config.Otel.Endpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return shutdown, xerrors.Newf("create metric exporter: %w", err)
	}
	shutdownFuncs = append(shutdownFuncs, metricExporter.Shutdown)

	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(
			metric.NewPeriodicReader(metricExporter,
				metric.WithInterval(time.Duration(config.Otel.Interval)), // matches prometheus scrape_interval
			),
		),
	)
	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)

	return shutdown, nil
}
