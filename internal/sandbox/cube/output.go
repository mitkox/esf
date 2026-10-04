package cube

import "fmt"

// boundedCommandScript uses FIFOs so each stream is capped before the Cube SDK
// sees it. The extra byte distinguishes an exact-limit result from overflow.
// A noisy process receives a closed pipe and the wrapper returns a distinct
// failure after preserving the bounded diagnostic prefix.
func boundedCommandScript(line string, limit int64) string {
	if limit < 2048 {
		limit = DefaultCommandOutputBytes
	}
	perStream := limit / 2
	return fmt.Sprintf(`
esf_output_dir=$(mktemp -d) || exit 125
trap 'rm -rf "$esf_output_dir"' EXIT
mkfifo "$esf_output_dir/out.pipe" "$esf_output_dir/err.pipe" || exit 125
head -c %d <"$esf_output_dir/out.pipe" >"$esf_output_dir/out" &
esf_out_pid=$!
head -c %d <"$esf_output_dir/err.pipe" >"$esf_output_dir/err" &
esf_err_pid=$!
(
%s
) >"$esf_output_dir/out.pipe" 2>"$esf_output_dir/err.pipe"
esf_exit=$?
wait "$esf_out_pid" || true
wait "$esf_err_pid" || true
head -c %d "$esf_output_dir/out"
head -c %d "$esf_output_dir/err" >&2
esf_out_size=$(wc -c <"$esf_output_dir/out")
esf_err_size=$(wc -c <"$esf_output_dir/err")
if [ "$esf_out_size" -gt %d ] || [ "$esf_err_size" -gt %d ]; then
  printf '\nESF_OUTPUT_LIMIT_EXCEEDED: command output exceeded %d bytes\n' >&2
  exit 122
fi
exit "$esf_exit"`, perStream+1, perStream+1, line, perStream, perStream, perStream, perStream, limit)
}
