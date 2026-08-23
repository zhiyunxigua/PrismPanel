plugins {
    id("com.gradleup.shadow")
}

repositories {
    maven("https://maven.fabricmc.net/")
}

dependencies {
    implementation(project(":core"))
    // 只依赖 fabric-loader 公开 API（net.fabricmc.loader.api / net.fabricmc.api），
    // 不引用任何 Minecraft 类，因此无需 fabric-loom / yarn mappings，
    // 产物可跨 MC 版本使用（要求 loader >= 0.14，Fabric Loader 0.15+ 本身要求 Java 17）。
    compileOnly("net.fabricmc:fabric-loader:0.16.9")
    testImplementation("org.junit.jupiter:junit-jupiter:5.11.4")
}

tasks.processResources {
    filesMatching("fabric.mod.json") {
        expand("version" to project.version)
    }
}

tasks.named<com.github.jengelman.gradle.plugins.shadow.tasks.ShadowJar>("shadowJar") {
    archiveFileName.set("prism-fabric-${project.version}.jar")
    relocate("com.google.gson", "com.xigua.prism.libs.gson")
}

tasks.build {
    dependsOn(tasks.shadowJar)
}
