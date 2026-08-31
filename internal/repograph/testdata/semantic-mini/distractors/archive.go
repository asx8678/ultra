package distractors

const ArchiveTopic = "ORDER_ARCHIVE_TOPIC"

type OrderArchive struct{}

func GetOlderOrder() string {
	return "/orders/archive"
}
