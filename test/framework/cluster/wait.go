package cluster

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	apierror "github.com/kubeclipper/kubeclipper/pkg/errors"
	corev1 "github.com/kubeclipper/kubeclipper/pkg/scheme/core/v1"
	"github.com/kubeclipper/kubeclipper/pkg/simple/client/kc"
	"github.com/kubeclipper/kubeclipper/test/framework"
)

const (
	// poll is how often to poll clusters.
	poll = 15 * time.Second
)

type timeoutError struct {
	msg             string
	observedObjects []interface{}
}

func (e *timeoutError) Error() string {
	return e.msg
}

func TimeoutError(msg string, observedObjects ...interface{}) error {
	return &timeoutError{
		msg:             msg,
		observedObjects: observedObjects,
	}
}

func IsTimeout(err error) bool {
	if err == wait.ErrWaitTimeout {
		return true
	}
	if _, ok := err.(*timeoutError); ok {
		return true
	}
	return false
}

type clusterCondition func(clu *corev1.Cluster) (bool, error)

type backupCondition func(backup *corev1.Backup) (bool, error)

// WaitForClusterCondition waits a cluster to be matched to the given condition.
func WaitForClusterCondition(c *kc.Client, clusterName, conditionDesc string, timeout time.Duration, condition clusterCondition) error {
	framework.Logf("Waiting up to %v for cluster %q to be %q", timeout, clusterName, conditionDesc)
	var (
		lastClusterError error
		lastCluster      *corev1.Cluster
		start            = time.Now()
	)
	err := wait.PollImmediate(poll, timeout, func() (bool, error) {
		clu, err := c.DescribeCluster(context.TODO(), clusterName)
		lastClusterError = err
		if err != nil || len(clu.Items) == 0 {
			return handleWaitingAPIError(err, true, "getting cluster %s", clusterName)
		}
		lastCluster = clu.Items[0].DeepCopy()
		framework.Logf("Cluster %q: Phase=%q, Elapsed: %v",
			clusterName, lastCluster.Status.Phase, time.Since(start))

		if done, err := condition(lastCluster); done {
			if err == nil {
				framework.Logf("Cluster %q satisfied condition %q", clusterName, conditionDesc)
			}
			return true, err
		} else if err != nil {
			framework.Logf("Error evaluating cluster condition %s: %v", conditionDesc, err)
		}
		return false, nil
	})
	if err == nil {
		return nil
	}
	if IsTimeout(err) && lastCluster != nil {
		return TimeoutError(fmt.Sprintf("timed out while waiting for cluster %s to be %s", clusterName, conditionDesc),
			lastCluster,
		)
	}
	if lastClusterError != nil {
		// If the last API call was an error.
		err = lastClusterError
	}
	return maybeTimeoutError(err, "waiting for cluster %s to be %s", clusterName, conditionDesc)
}

func WaitForBackupCondition(c *kc.Client, clusterName, backupName, conditionDesc string, timeout time.Duration, condition backupCondition) error {
	framework.Logf("Waiting up to %v for backup %q to be %q", timeout, backupName, conditionDesc)
	bp := &corev1.Backup{}
	start := time.Now()
	err := wait.PollImmediate(poll, timeout, func() (bool, error) {
		backups, apiErr := c.ListBackupsWithCluster(context.TODO(), clusterName)
		if apiErr != nil || len(backups.Items) == 0 {
			return handleWaitingAPIError(apiErr, true, "getting backup %s", backupName)
		}
		bp = backups.Items[0].DeepCopy()
		framework.Logf("Backup %q: Phase=%q, Elapsed: %v", backupName, bp.Status.ClusterBackupStatus, time.Since(start))
		if done, conErr := condition(bp); done {
			if conErr == nil {
				framework.Logf("Backup %q satisfied condition %q", backupName, conditionDesc)
			}
			return true, conErr
		} else if conErr != nil {
			framework.Logf("Error evaluating backup condition %s: %v", conditionDesc, conErr)
		}
		return false, nil
	})
	if err == nil {
		return nil
	}
	if IsTimeout(err) && bp != nil {
		return TimeoutError(fmt.Sprintf("timed out while waiting for backup %s to be %s", backupName, conditionDesc), bp)
	}
	return maybeTimeoutError(err, "waiting for backup %s to be %s", backupName, conditionDesc)
}

func WaitForClusterRunning(c *kc.Client, clusterName string, timeout time.Duration) error {
	return WaitForClusterCondition(c, clusterName, fmt.Sprintf("cluster %s running", clusterName), timeout, func(clu *corev1.Cluster) (bool, error) {
		return clu.Status.Phase == corev1.ClusterRunning, nil
	})
}

func WaitForClusterHealthy(c *kc.Client, clusterName string, timeout time.Duration) error {
	return WaitForClusterCondition(c, clusterName, fmt.Sprintf("cluster %s healthy", clusterName), timeout, func(clu *corev1.Cluster) (bool, error) {
		for _, item := range clu.Status.ComponentConditions {
			if item.Name == "kubernetes" {
				return item.Status == corev1.ComponentHealthy, nil
			}
		}
		return false, nil
	})
}

// WaitForClusterNotFound returns an error if it takes too long for the pod to fully terminate.
// Unlike `waitForPodTerminatedInNamespace`, the pod's Phase and Reason are ignored. If the pod Get
// api returns IsNotFound then the wait stops and nil is returned. If the Get api returns an error other
// than "not found" then that error is returned and the wait stops.
func WaitForClusterNotFound(c *kc.Client, clusterName string, timeout time.Duration) error {
	var lastCluster *corev1.Cluster
	err := wait.PollImmediate(poll, timeout, func() (done bool, err error) {
		clu, err := c.DescribeCluster(context.TODO(), clusterName)
		if apierror.IsNotFound(err) {
			// done
			return true, nil
		}
		if err != nil {
			return handleWaitingAPIError(err, true, "getting cluster %s", clusterName)
		}
		if len(clu.Items) == 0 {
			framework.Logf("unexpected problem, cluster not be nil at this time")
			return false, nil
		}
		lastCluster = clu.Items[0].DeepCopy()
		return false, nil
	})
	if err == nil {
		return nil
	}
	if IsTimeout(err) && lastCluster != nil {
		return TimeoutError(fmt.Sprintf("timeout while waiting for cluster %s to be Not Found", clusterName), lastCluster)
	}
	return maybeTimeoutError(err, "waiting for cluster %s not found", clusterName)
}

func WaitForComponentNotFound(c *kc.Client, clusterName string, timeout time.Duration) error {
	var lastCluster *corev1.Cluster
	err := wait.PollImmediate(poll, timeout, func() (done bool, err error) {
		clu, err := c.DescribeCluster(context.TODO(), clusterName)
		if err != nil {
			return handleWaitingAPIError(err, true, "getting cluster %s", clusterName)
		}
		if len(clu.Items) == 0 {
			framework.Logf("unexpected problem, cluster not be nil at this time")
			return false, nil
		}
		lastCluster = clu.Items[0].DeepCopy()
		if len(lastCluster.Addons) == 0 {
			return true, nil
		}
		return false, nil
	})
	if err == nil {
		return nil
	}
	if IsTimeout(err) && lastCluster != nil {
		return TimeoutError(fmt.Sprintf("timeout while waiting for cluster %s component to be Not Found", clusterName), lastCluster)
	}
	return maybeTimeoutError(err, "waiting for cluster %s uninstall component not found", clusterName)
}

func WaitForBackupAvailable(c *kc.Client, clusterName, backupName string, timeout time.Duration) error {
	return WaitForBackupCondition(c, clusterName, backupName, fmt.Sprintf("backup %s available", backupName), timeout, func(backup *corev1.Backup) (bool, error) {
		if backup.Status.ClusterBackupStatus == corev1.ClusterBackupAvailable {
			return true, nil
		} else if backup.Status.ClusterBackupStatus == corev1.ClusterBackupError {
			return false, fmt.Errorf("backup %s create failed", backup.Name)
		} else {
			return false, nil
		}
	})
}

func WaitForBackupNotFound(c *kc.Client, clusterName, backupName string, timeout time.Duration) error {
	bp := &corev1.Backup{}
	err := wait.PollImmediate(poll, timeout, func() (done bool, err error) {
		backups, waitErr := c.ListBackupsWithCluster(context.TODO(), clusterName)
		if waitErr != nil {
			return handleWaitingAPIError(waitErr, true, "getting backup %s", backupName)
		}
		if len(backups.Items) == 0 {
			return true, nil
		}
		bp = backups.Items[0].DeepCopy()
		return false, nil
	})
	if err == nil {
		return nil
	}
	if IsTimeout(err) && bp != nil {
		return TimeoutError(fmt.Sprintf("timeout while waiting for backup %s to be Not Found", backupName), bp)
	}
	return maybeTimeoutError(err, "waiting for backup %s not found", backupName)
}

func WaitForRecovery(c *kc.Client, clusterName string, timeout time.Duration) error {
	return WaitForClusterCondition(c, clusterName, "recovery successful", timeout, func(clu *corev1.Cluster) (bool, error) {
		if clu.Status.Phase == corev1.ClusterRunning {
			return true, nil
		} else if clu.Status.Phase == corev1.ClusterRestoreFailed {
			return false, fmt.Errorf("recovery cluster %s failed", clusterName)
		}
		return false, nil
	})
}

// maybeTimeoutError returns a TimeoutError if err is a timeout. Otherwise, wrap err.
// taskFormat and taskArgs should be the task being performed when the error occurred,
// e.g. "waiting for pod to be running".
func maybeTimeoutError(err error, taskFormat string, taskArgs ...interface{}) error {
	if IsTimeout(err) {
		return TimeoutError(fmt.Sprintf("timed out while "+taskFormat, taskArgs...))
	} else if err != nil {
		return fmt.Errorf("error while %s: %w", fmt.Sprintf(taskFormat, taskArgs...), err)
	} else {
		return nil
	}
}

// handleWaitingAPIErrror handles an error from an API request in the context of a Wait function.
// If the error is retryable, sleep the recommended delay and ignore the error.
// If the erorr is terminal, return it.
func handleWaitingAPIError(err error, retryNotFound bool, taskFormat string, taskArgs ...interface{}) (bool, error) {
	taskDescription := fmt.Sprintf(taskFormat, taskArgs...)
	if retryNotFound && apierror.IsNotFound(err) {
		framework.Logf("Ignoring NotFound error while " + taskDescription)
		return false, nil
	}
	if retry, delay := shouldRetry(err); retry {
		framework.Logf("Retryable error while %s, retrying after %v: %v", taskDescription, delay, err)
		if delay > 0 {
			time.Sleep(delay)
		}
		return false, nil
	}
	framework.Logf("Encountered non-retryable error while %s: %v", taskDescription, err)
	return false, err
}

// Decide whether to retry an API request. Optionally include a delay to retry after.
func shouldRetry(err error) (retry bool, retryAfter time.Duration) {
	// these errors indicate a transient error that should be retried.
	if apierror.IsInternalError(err) || apierror.IsTooManyRequests(err) {
		return true, 0
	}

	return false, 0
}
