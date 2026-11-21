package awsinv

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

func atoi64(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

func collectSQS(ctx context.Context, s *scope) {
	c := s.c.SQS
	const svc = SvcSQS
	p := sqs.NewListQueuesPaginator(c, &sqs.ListQueuesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListQueues", err, "SQS queues were not inventoried", "")
			return
		}
		for _, qurl := range page.QueueUrls {
			name := qurl[strings.LastIndex(qurl, "/")+1:]
			arn := s.arn("sqs", name)
			out, err := c.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{QueueUrl: aws.String(qurl), AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll}})
			if err != nil {
				s.gap(svc, "GetQueueAttributes", err, "Queue configuration (FIFO, DLQ, retention, KMS) unknown", arn)
				s.add(inventory.Resource{ID: arn, Type: "AWS::SQS::Queue", Name: name, Completeness: inventory.CompletenessIdentifierOnly,
					Attributes: map[string]any{"queue_url": qurl, "fifo_queue": strings.HasSuffix(name, ".fifo")}})
				continue
			}
			at := out.Attributes
			arn = firstNonEmpty(at["QueueArn"], arn)
			a := attrs{}.s("queue_url", qurl).any("fifo_queue", at["FifoQueue"] == "true" || strings.HasSuffix(name, ".fifo")).
				s("kms_master_key_id", at["KmsMasterKeyId"]).any("sqs_managed_sse_enabled", at["SqsManagedSseEnabled"] == "true").
				any("has_queue_policy", at["Policy"] != "")
			if at["FifoQueue"] == "true" {
				a.any("content_based_deduplication", at["ContentBasedDeduplication"] == "true")
			}
			for k, attr := range map[string]string{"visibility_timeout_seconds": "VisibilityTimeout", "message_retention_seconds": "MessageRetentionPeriod",
				"delay_seconds": "DelaySeconds", "max_message_size_bytes": "MaximumMessageSize", "receive_wait_time_seconds": "ReceiveMessageWaitTimeSeconds",
				"approximate_number_of_messages": "ApproximateNumberOfMessages"} {
				if n, ok := atoi64(at[attr]); ok {
					a[k] = n
				}
			}
			if rp := at["RedrivePolicy"]; rp != "" {
				var red struct {
					DeadLetterTargetArn string `json:"deadLetterTargetArn"`
					MaxReceiveCount     any    `json:"maxReceiveCount"`
				}
				if json.Unmarshal([]byte(rp), &red) == nil {
					m := map[string]any{"dead_letter_target_arn": red.DeadLetterTargetArn}
					switch v := red.MaxReceiveCount.(type) {
					case float64:
						m["max_receive_count"] = int64(v)
					case string:
						if n, ok := atoi64(v); ok {
							m["max_receive_count"] = n
						}
					}
					a.any("redrive_policy", m)
					s.rel(arn, red.DeadLetterTargetArn, inventory.RelDependsOn)
				}
			}
			if k := at["KmsMasterKeyId"]; k != "" {
				s.rel(arn, s.kmsRef(k), inventory.RelEncryptedBy)
			}
			r := inventory.Resource{ID: arn, Type: "AWS::SQS::Queue", Name: name, Attributes: a.m()}
			if tg, err := c.ListQueueTags(ctx, &sqs.ListQueueTagsInput{QueueUrl: aws.String(qurl)}); err != nil {
				s.gap(svc, "ListQueueTags", err, "Tags unknown", arn)
			} else if len(tg.Tags) > 0 {
				r.Tags = tg.Tags
			}
			s.add(r)
		}
	}
}

func collectSNS(ctx context.Context, s *scope) {
	c := s.c.SNS
	const svc = SvcSNS
	p := sns.NewListTopicsPaginator(c, &sns.ListTopicsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListTopics", err, "SNS topics and subscriptions were not inventoried", "")
			return
		}
		for _, t := range page.Topics {
			arn := aws.ToString(t.TopicArn)
			name := arn[strings.LastIndex(arn, ":")+1:]
			r := inventory.Resource{ID: arn, Type: "AWS::SNS::Topic", Name: name}
			if out, err := c.GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{TopicArn: aws.String(arn)}); err != nil {
				s.gap(svc, "GetTopicAttributes", err, "Topic configuration unknown", arn)
			} else {
				at := out.Attributes
				a := attrs{}.s("display_name", at["DisplayName"]).any("fifo_topic", at["FifoTopic"] == "true").
					s("kms_master_key_id", at["KmsMasterKeyId"]).any("has_topic_policy", at["Policy"] != "")
				for k, attr := range map[string]string{"subscriptions_confirmed": "SubscriptionsConfirmed", "subscriptions_pending": "SubscriptionsPending"} {
					if n, ok := atoi64(at[attr]); ok {
						a[k] = n
					}
				}
				if k := at["KmsMasterKeyId"]; k != "" {
					s.rel(arn, s.kmsRef(k), inventory.RelEncryptedBy)
				}
				r.Attributes = a.m()
			}
			if tg, err := c.ListTagsForResource(ctx, &sns.ListTagsForResourceInput{ResourceArn: aws.String(arn)}); err != nil {
				s.gap(svc, "ListTagsForResource", err, "Tags unknown", arn)
			} else {
				r.Tags = tagMap(tg.Tags, func(t snstypes.Tag) (*string, *string) { return t.Key, t.Value })
			}
			s.add(r)
		}
	}
	sp := sns.NewListSubscriptionsPaginator(c, &sns.ListSubscriptionsInput{})
	for sp.HasMorePages() {
		page, err := sp.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListSubscriptions", err, "SNS subscriptions (fan-out to queues/functions) unknown", "")
			return
		}
		for _, sub := range page.Subscriptions {
			arn := aws.ToString(sub.SubscriptionArn)
			topic := aws.ToString(sub.TopicArn)
			proto := aws.ToString(sub.Protocol)
			endpoint := aws.ToString(sub.Endpoint)
			if !strings.HasPrefix(arn, "arn:") {
				continue // PendingConfirmation / Deleted have no ARN
			}
			a := attrs{}.s("protocol", proto).s("topic_arn", topic)
			// Endpoints of email/sms/http(s)/application subscriptions can be personal data or
			// URLs with credentials; only AWS resource endpoints are kept.
			switch proto {
			case "sqs", "lambda", "firehose":
				a.s("endpoint", endpoint)
				s.rel(endpoint, topic, inventory.RelSubscribesTo)
			default:
				a.any("endpoint_redacted", true)
			}
			if out, err := c.GetSubscriptionAttributes(ctx, &sns.GetSubscriptionAttributesInput{SubscriptionArn: aws.String(arn)}); err != nil {
				s.gap(svc, "GetSubscriptionAttributes", err, "Subscription filter/raw delivery settings unknown", arn)
			} else {
				at := out.Attributes
				a.any("raw_message_delivery", at["RawMessageDelivery"] == "true").any("has_filter_policy", at["FilterPolicy"] != "")
				if rp := at["RedrivePolicy"]; rp != "" {
					var red struct {
						DeadLetterTargetArn string `json:"deadLetterTargetArn"`
					}
					if json.Unmarshal([]byte(rp), &red) == nil && red.DeadLetterTargetArn != "" {
						a.s("dead_letter_target_arn", red.DeadLetterTargetArn)
						s.rel(arn, red.DeadLetterTargetArn, inventory.RelDependsOn)
					}
				}
			}
			s.rel(arn, topic, inventory.RelSubscribesTo)
			name := topic[strings.LastIndex(topic, ":")+1:] + "/" + proto
			if proto == "sqs" || proto == "lambda" {
				name = topic[strings.LastIndex(topic, ":")+1:] + "->" + endpoint[strings.LastIndex(endpoint, ":")+1:]
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::SNS::Subscription", Name: name, Attributes: a.m()})
		}
	}
}

func collectKinesis(ctx context.Context, s *scope) {
	c := s.c.Kinesis
	const svc = SvcKinesis
	p := kinesis.NewListStreamsPaginator(c, &kinesis.ListStreamsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListStreams", err, "Kinesis data streams were not inventoried", "")
			return
		}
		for _, name := range page.StreamNames {
			arn := s.arn("kinesis", "stream/"+name)
			out, err := c.DescribeStreamSummary(ctx, &kinesis.DescribeStreamSummaryInput{StreamName: aws.String(name)})
			if err != nil {
				s.gap(svc, "DescribeStreamSummary", err, "Stream configuration unknown", arn)
				s.add(inventory.Resource{ID: arn, Type: "AWS::Kinesis::Stream", Name: name, Completeness: inventory.CompletenessIdentifierOnly})
				continue
			}
			d := out.StreamDescriptionSummary
			arn = firstNonEmpty(aws.ToString(d.StreamARN), arn)
			a := attrs{}.s("stream_name", name).s("status", string(d.StreamStatus)).i32("open_shard_count", d.OpenShardCount).
				i32("retention_hours", d.RetentionPeriodHours).s("encryption_type", string(d.EncryptionType)).str("kms_key_id", d.KeyId).
				i32("consumer_count", d.ConsumerCount)
			if d.StreamModeDetails != nil {
				a.s("stream_mode", string(d.StreamModeDetails.StreamMode))
			}
			if k := aws.ToString(d.KeyId); k != "" {
				s.rel(arn, s.kmsRef(k), inventory.RelEncryptedBy)
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::Kinesis::Stream", Name: name, Attributes: a.m()})
		}
	}
}

func collectEvents(ctx context.Context, s *scope) {
	c := s.c.EventBridge
	const svc = SvcEvents
	var token *string
	for {
		out, err := c.ListEventBuses(ctx, &eventbridge.ListEventBusesInput{NextToken: token})
		if err != nil {
			s.gap(svc, "ListEventBuses", err, "EventBridge buses and rules were not inventoried", "")
			return
		}
		for _, b := range out.EventBuses {
			bus := aws.ToString(b.Name)
			barn := aws.ToString(b.Arn)
			s.add(inventory.Resource{ID: barn, Type: "AWS::Events::EventBus", Name: bus, Attributes: attrs{}.s("bus_name", bus).any("has_policy", aws.ToString(b.Policy) != "").m()})
			collectRules(ctx, s, bus, barn)
		}
		if aws.ToString(out.NextToken) == "" {
			return
		}
		token = out.NextToken
	}
}

func collectRules(ctx context.Context, s *scope, bus, busARN string) {
	c := s.c.EventBridge
	const svc = SvcEvents
	var token *string
	for {
		out, err := c.ListRules(ctx, &eventbridge.ListRulesInput{EventBusName: aws.String(bus), NextToken: token})
		if err != nil {
			s.gap(svc, "ListRules", err, "Rules on bus "+bus+" unknown", busARN)
			return
		}
		for _, r := range out.Rules {
			arn := aws.ToString(r.Arn)
			name := aws.ToString(r.Name)
			a := attrs{}.s("event_bus_name", bus).s("rule_name", name).s("state", string(r.State)).str("schedule_expression", r.ScheduleExpression).
				any("has_event_pattern", aws.ToString(r.EventPattern) != "").str("role_arn", r.RoleArn).str("managed_by", r.ManagedBy)
			s.rel(arn, busARN, inventory.RelAttachedTo)
			tout, err := c.ListTargetsByRule(ctx, &eventbridge.ListTargetsByRuleInput{Rule: aws.String(name), EventBusName: aws.String(bus)})
			if err != nil {
				s.gap(svc, "ListTargetsByRule", err, "Rule targets unknown", arn)
			} else {
				var targets []string
				for _, t := range tout.Targets {
					targets = append(targets, aws.ToString(t.Arn))
					s.rel(arn, aws.ToString(t.Arn), inventory.RelTargets)
				}
				a.list("target_arns", targets)
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::Events::Rule", Name: name, Attributes: a.m()})
		}
		if aws.ToString(out.NextToken) == "" {
			return
		}
		token = out.NextToken
	}
}

func collectSFN(ctx context.Context, s *scope) {
	c := s.c.SFN
	const svc = SvcSFN
	p := sfn.NewListStateMachinesPaginator(c, &sfn.ListStateMachinesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListStateMachines", err, "Step Functions state machines were not inventoried", "")
			return
		}
		for _, sm := range page.StateMachines {
			arn := aws.ToString(sm.StateMachineArn)
			a := attrs{}.str("state_machine_name", sm.Name).s("type", string(sm.Type))
			out, err := c.DescribeStateMachine(ctx, &sfn.DescribeStateMachineInput{StateMachineArn: aws.String(arn)})
			if err != nil {
				s.gap(svc, "DescribeStateMachine", err, "State machine role and targets unknown", arn)
			} else {
				a.str("role_arn", out.RoleArn).s("status", string(out.Status))
				if out.LoggingConfiguration != nil {
					a.s("logging_level", string(out.LoggingConfiguration.Level))
				}
				if out.TracingConfiguration != nil {
					a.any("tracing_enabled", out.TracingConfiguration.Enabled)
				}
				s.rel(arn, aws.ToString(out.RoleArn), inventory.RelAssumesRole)
				// The definition is parsed for resource ARNs and then discarded.
				for _, target := range arnsIn(aws.ToString(out.Definition)) {
					s.relE(arn, target, inventory.RelTargets, inventory.EvidenceInferred)
				}
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::StepFunctions::StateMachine", Name: aws.ToString(sm.Name), Attributes: a.m()})
		}
	}
}
