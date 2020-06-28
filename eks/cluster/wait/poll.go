// Package wait implements cluster waiter.
package wait

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-k8s-tester/eksconfig"
	"github.com/aws/aws-k8s-tester/pkg/ctxutil"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/eks"
	aws_eks "github.com/aws/aws-sdk-go/service/eks"
	"github.com/aws/aws-sdk-go/service/eks/eksiface"
	"github.com/dustin/go-humanize"
	"go.uber.org/zap"
)

// IsDeleted returns true if error from EKS API indicates that
// the EKS cluster has already been deleted.
func IsDeleted(err error) bool {
	if err == nil {
		return false
	}
	awsErr, ok := err.(awserr.Error)
	if ok && awsErr.Code() == "ResourceNotFoundException" &&
		strings.HasPrefix(awsErr.Message(), "No cluster found for") {
		return true
	}
	// ResourceNotFoundException: No cluster found for name: aws-k8s-tester-155468BC717E03B003\n\tstatus code: 404, request id: 1e3fe41c-b878-11e8-adca-b503e0ba731d
	return strings.Contains(err.Error(), "No cluster found for name: ")
}

// ClusterStatus represents the EKS cluster status.
type ClusterStatus struct {
	Cluster *aws_eks.Cluster
	Error   error
}

// Poll periodically fetches the cluster status
// until the cluster becomes the desired state.
func Poll(
	ctx context.Context,
	stopc chan struct{},
	lg *zap.Logger,
	eksAPI eksiface.EKSAPI,
	clusterName string,
	desiredClusterStatus string,
	initialWait time.Duration,
	pollInterval time.Duration,
	opts ...OpOption) <-chan ClusterStatus {

	ret := Op{}
	ret.applyOpts(opts)

	lg.Info("polling cluster",
		zap.String("cluster-name", clusterName),
		zap.String("desired-status", desiredClusterStatus),
		zap.String("initial-wait", initialWait.String()),
		zap.String("poll-interval", pollInterval.String()),
		zap.String("ctx-time-left", ctxutil.TimeLeftTillDeadline(ctx)),
	)

	now := time.Now()

	ch := make(chan ClusterStatus, 10)
	go func() {
		// very first poll should be no-wait
		// in case stack has already reached desired status
		// wait from second interation
		waitDur := time.Duration(0)

		first := true
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
				lg.Warn("wait aborted", zap.Error(ctx.Err()))
				ch <- ClusterStatus{Cluster: nil, Error: ctx.Err()}
				close(ch)
				return

			case <-stopc:
				lg.Warn("wait stopped", zap.Error(ctx.Err()))
				ch <- ClusterStatus{Cluster: nil, Error: errors.New("wait stopped")}
				close(ch)
				return

			case <-time.After(waitDur):
				// very first poll should be no-wait
				// in case stack has already reached desired status
				// wait from second interation
				if waitDur == time.Duration(0) {
					waitDur = pollInterval
				}
			}

			output, err := eksAPI.DescribeCluster(&aws_eks.DescribeClusterInput{
				Name: aws.String(clusterName),
			})
			if err != nil {
				if IsDeleted(err) {
					if desiredClusterStatus == eksconfig.ClusterStatusDELETEDORNOTEXIST {
						lg.Info("cluster is already deleted as desired; exiting", zap.Error(err))
						ch <- ClusterStatus{Cluster: nil, Error: nil}
						close(ch)
						return
					}
					lg.Warn("cluster does not exist; aborting", zap.Error(err))
					ch <- ClusterStatus{Cluster: nil, Error: err}
					close(ch)
					return
				}
				lg.Warn("describe cluster failed; retrying", zap.Error(err))
				ch <- ClusterStatus{Cluster: nil, Error: err}
				continue
			}

			if output.Cluster == nil {
				lg.Warn("expected non-nil cluster; retrying")
				ch <- ClusterStatus{Cluster: nil, Error: fmt.Errorf("unexpected empty response %+v", output.GoString())}
				continue
			}

			cluster := output.Cluster
			currentStatus := aws.StringValue(cluster.Status)
			lg.Info("poll",
				zap.String("cluster-name", clusterName),
				zap.String("status", currentStatus),
				zap.String("started", humanize.RelTime(now, time.Now(), "ago", "from now")),
				zap.String("ctx-time-left", ctxutil.TimeLeftTillDeadline(ctx)),
			)
			switch currentStatus {
			case desiredClusterStatus:
				ch <- ClusterStatus{Cluster: cluster, Error: nil}
				lg.Info("desired cluster status; done", zap.String("status", currentStatus))
				close(ch)
				return
			case aws_eks.ClusterStatusFailed:
				ch <- ClusterStatus{Cluster: cluster, Error: fmt.Errorf("unexpected cluster status %q", aws_eks.ClusterStatusFailed)}
				lg.Warn("cluster status failed", zap.String("status", currentStatus), zap.String("desired-status", desiredClusterStatus))
				close(ch)
				return
			default:
				ch <- ClusterStatus{Cluster: cluster, Error: nil}
			}

			if ret.queryFunc != nil {
				ret.queryFunc()
			}

			if first {
				lg.Info("sleeping", zap.Duration("initial-wait", initialWait))
				select {
				case <-ctx.Done():
					lg.Warn("wait aborted", zap.Error(ctx.Err()))
					ch <- ClusterStatus{Cluster: nil, Error: ctx.Err()}
					close(ch)
					return
				case <-stopc:
					lg.Warn("wait stopped", zap.Error(ctx.Err()))
					ch <- ClusterStatus{Cluster: nil, Error: errors.New("wait stopped")}
					close(ch)
					return
				case <-time.After(initialWait):
				}
				first = false
			}
		}

		lg.Warn("wait aborted", zap.Error(ctx.Err()))
		ch <- ClusterStatus{Cluster: nil, Error: ctx.Err()}
		close(ch)
		return
	}()
	return ch
}

// updateNotExists returns true if error from EKS API indicates that
// the EKS cluster update does not exist.
func updateNotExists(err error) bool {
	if err == nil {
		return false
	}
	awsErr, ok := err.(awserr.Error)
	if ok && awsErr.Code() == "ResourceNotFoundException" &&
		strings.HasPrefix(awsErr.Message(), "No update found for") {
		return true
	}
	// An error occurred (ResourceNotFoundException) when calling the DescribeUpdate operation: No update found for ID: 10bddb13-a71b-425a-b0a6-71cd03e59161
	return strings.Contains(err.Error(), "No update found")
}

// UpdateStatus represents the CloudFormation status.
type UpdateStatus struct {
	Update *eks.Update
	Error  error
}

// PollUpdate periodically fetches the cluster update status
// until the cluster update becomes the desired state.
// ref. https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeUpdate.html
func PollUpdate(
	ctx context.Context,
	stopc chan struct{},
	lg *zap.Logger,
	eksAPI eksiface.EKSAPI,
	clusterName string,
	requestID string,
	desiredUpdateStatus string,
	initialWait time.Duration,
	pollInterval time.Duration,
	opts ...OpOption) <-chan UpdateStatus {

	ret := Op{}
	ret.applyOpts(opts)

	lg.Info("polling cluster update",
		zap.String("cluster-name", clusterName),
		zap.String("request-id", requestID),
		zap.String("desired-update-status", desiredUpdateStatus),
		zap.String("initial-wait", initialWait.String()),
		zap.String("poll-interval", pollInterval.String()),
		zap.String("ctx-time-left", ctxutil.TimeLeftTillDeadline(ctx)),
	)

	now := time.Now()

	ch := make(chan UpdateStatus, 10)
	go func() {
		// very first poll should be no-wait
		// in case stack has already reached desired status
		// wait from second interation
		waitDur := time.Duration(0)

		first := true
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
				lg.Warn("wait aborted", zap.Error(ctx.Err()))
				ch <- UpdateStatus{Update: nil, Error: ctx.Err()}
				close(ch)
				return

			case <-stopc:
				lg.Warn("wait stopped", zap.Error(ctx.Err()))
				ch <- UpdateStatus{Update: nil, Error: errors.New("wait stopped")}
				close(ch)
				return

			case <-time.After(waitDur):
				// very first poll should be no-wait
				// in case stack has already reached desired status
				// wait from second interation
				if waitDur == time.Duration(0) {
					waitDur = pollInterval
				}
			}

			output, err := eksAPI.DescribeUpdate(&eks.DescribeUpdateInput{
				Name:     aws.String(clusterName),
				UpdateId: aws.String(requestID),
			})
			if err != nil {
				if updateNotExists(err) {
					lg.Warn("cluster update does not exist; aborting", zap.Error(ctx.Err()))
					ch <- UpdateStatus{Update: nil, Error: err}
					close(ch)
					return
				}

				lg.Warn("describe cluster update failed; retrying", zap.Error(err))
				ch <- UpdateStatus{Update: nil, Error: err}
				continue
			}

			if output.Update == nil {
				lg.Warn("expected non-nil cluster update; retrying")
				ch <- UpdateStatus{Update: nil, Error: fmt.Errorf("unexpected empty response %+v", output.GoString())}
				continue
			}

			update := output.Update
			currentStatus := aws.StringValue(update.Status)
			updateType := aws.StringValue(update.Type)
			lg.Info("poll",
				zap.String("cluster-name", clusterName),
				zap.String("status", currentStatus),
				zap.String("update-type", updateType),
				zap.String("started", humanize.RelTime(now, time.Now(), "ago", "from now")),
				zap.String("ctx-time-left", ctxutil.TimeLeftTillDeadline(ctx)),
			)
			switch currentStatus {
			case desiredUpdateStatus:
				ch <- UpdateStatus{Update: update, Error: nil}
				lg.Info("desired cluster update status; done", zap.String("status", currentStatus))
				close(ch)
				return
			case eks.UpdateStatusCancelled:
				ch <- UpdateStatus{Update: update, Error: fmt.Errorf("unexpected cluster update status %q", eks.UpdateStatusCancelled)}
				lg.Warn("cluster update status cancelled", zap.String("status", currentStatus), zap.String("desired-status", desiredUpdateStatus))
				close(ch)
				return
			case eks.UpdateStatusFailed:
				ch <- UpdateStatus{Update: update, Error: fmt.Errorf("unexpected cluster update status %q", eks.UpdateStatusFailed)}
				lg.Warn("cluster update status failed", zap.String("status", currentStatus), zap.String("desired-status", desiredUpdateStatus))
				close(ch)
				return
			default:
				ch <- UpdateStatus{Update: update, Error: nil}
			}

			if ret.queryFunc != nil {
				ret.queryFunc()
			}

			if first {
				lg.Info("sleeping", zap.Duration("initial-wait", initialWait))
				select {
				case <-ctx.Done():
					lg.Warn("wait aborted", zap.Error(ctx.Err()))
					ch <- UpdateStatus{Update: nil, Error: ctx.Err()}
					close(ch)
					return
				case <-stopc:
					lg.Warn("wait stopped", zap.Error(ctx.Err()))
					ch <- UpdateStatus{Update: nil, Error: errors.New("wait stopped")}
					close(ch)
					return
				case <-time.After(initialWait):
				}
				first = false
			}
		}

		lg.Warn("wait aborted", zap.Error(ctx.Err()))
		ch <- UpdateStatus{Update: nil, Error: ctx.Err()}
		close(ch)
		return
	}()
	return ch
}

// Op represents a MNG operation.
type Op struct {
	queryFunc func()
}

// OpOption configures archiver operations.
type OpOption func(*Op)

// WithQueryFunc configures query function to be called in retry func.
func WithQueryFunc(f func()) OpOption {
	return func(op *Op) { op.queryFunc = f }
}

func (op *Op) applyOpts(opts []OpOption) {
	for _, opt := range opts {
		opt(op)
	}
}
