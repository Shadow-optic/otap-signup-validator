const std = @import("std");
pub fn build(b: *std.Build) void {
    const lib = b.addSharedLibrary(.{
        .name = "otap_mmio",
        .root_source_file = b.path("src/mmio.zig"),
        .optimize = .ReleaseFast,
        .target = b.host(),
    });
    b.installArtifact(lib);
}
